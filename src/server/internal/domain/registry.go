// Plans conservative regulation identity candidates from source observations.
//
// The planner consumes a validated DocumentBatch and emits exact natural-key candidates plus
// explicit review items. It never allocates canonical IDs, writes a registry, infers legal dates,
// or merges records from text similarity. Corpus policy supplies jurisdiction explicitly.
// Runtime includes bounded wire traversal, O(S log S + O log O + sum(V log V)) ordering, and
// linear string normalization; benchmark batch latency and false-merge/false-split quality against
// configs/benchmark-targets.yaml. Required targets remain REQUIRED_UNMEASURED.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const RegulationIdentityKeyNamespace = "regulation-natural-key:v1"
const CanonicalEntityTypeRegulation int16 = 1

// RegulationIdentityPolicy holds facts configured at the corpus boundary. Jurisdiction is not
// derived from a portal hostname because one portal may contain national and regional rules.
type RegulationIdentityPolicy struct {
	Jurisdiction string
	MaximumItems int
}

// RegulationIdentityCandidate is ready for an atomic registry lookup/allocation. IssuerLabel is
// still a sourced label; the registry must resolve it to a stable issuer ID before Regulation is
// publishable. IdentityKey is an opaque hash over every exact-key component.
type RegulationIdentityCandidate struct {
	SourceBlobID   string
	ObservationIDs []string
	Kind           string
	IssuerLabel    string
	Jurisdiction   string
	OfficialNumber string
	Year           uint32
	Title          string
	IdentityKey    string
}

// RegulationIdentityPlan partitions every source blob into either a complete exact candidate or
// a review item. A blob never appears in both collections.
type RegulationIdentityPlan struct {
	Candidates []RegulationIdentityCandidate
	Reviews    []*pb.ReviewItem
}

// CanonicalIdentityClaim is the immutable input to one atomic registry operation. PayloadHash
// binds the complete proposal, while IdentityKey binds only the exact natural identity.
type CanonicalIdentityClaim struct {
	ProposalKey   string
	EntityType    int16
	IdentityScope string
	IdentityKey   string
	PayloadHash   string
}

type CanonicalIdentityAssignment struct {
	ProposalKey string
	CanonicalID string
	Revision    uint64
	Created     bool
}

// CanonicalClaim binds this sourced proposal for idempotent allocation in the registry. It
// recomputes the natural key so a caller cannot mutate fields while retaining a stale key.
func (candidate RegulationIdentityCandidate) CanonicalClaim() (CanonicalIdentityClaim, error) {
	if candidate.SourceBlobID == "" || len(candidate.ObservationIDs) == 0 || candidate.Kind == "" ||
		candidate.IssuerLabel == "" || candidate.Jurisdiction == "" || candidate.OfficialNumber == "" ||
		candidate.Year == 0 || candidate.Title == "" {
		return CanonicalIdentityClaim{}, errors.New("complete regulation identity candidate is required")
	}
	identityKey := regulationIdentityKey(candidate)
	if candidate.IdentityKey != identityKey {
		return CanonicalIdentityClaim{}, errors.New("regulation identity candidate key is stale or forged")
	}
	payload, _ := json.Marshal(candidate)
	payloadSum := sha256.Sum256(payload)
	proposalSum := sha256.Sum256([]byte(candidate.SourceBlobID + "\x00" + candidate.IdentityKey))
	return CanonicalIdentityClaim{
		ProposalKey: "regulation-proposal:" + hex.EncodeToString(proposalSum[:]),
		EntityType:  CanonicalEntityTypeRegulation, IdentityScope: RegulationIdentityKeyNamespace,
		IdentityKey: candidate.IdentityKey, PayloadHash: hex.EncodeToString(payloadSum[:]),
	}, nil
}

// PlanRegulationIdentities extracts exact identity fields without treating portal assertions as
// verified truth. Equivalent input order produces byte-identical candidate and review ordering.
func PlanRegulationIdentities(batch *pb.DocumentBatch, policy RegulationIdentityPolicy) (RegulationIdentityPlan, error) {
	if batch == nil || batch.GetMeta() == nil || strings.TrimSpace(policy.Jurisdiction) == "" || policy.MaximumItems <= 0 {
		return RegulationIdentityPlan{}, errors.New("document batch, jurisdiction, and positive item limit are required")
	}
	if len(batch.Sources) > policy.MaximumItems || len(batch.Observations) > policy.MaximumItems {
		return RegulationIdentityPlan{}, errors.New("regulation identity input exceeds configured item limit")
	}
	if err := ValidateWire(batch, DefaultWireLimits); err != nil {
		return RegulationIdentityPlan{}, fmt.Errorf("validate identity input: %w", err)
	}

	observations := make(map[string][]*pb.SourceObservation, len(batch.Sources))
	seenObservations := make(map[string]bool, len(batch.Observations))
	for _, observation := range batch.Observations {
		observationID := observation.GetMeta().GetRecordId()
		if seenObservations[observationID] {
			return RegulationIdentityPlan{}, errors.New("duplicate source observation in identity input")
		}
		seenObservations[observationID] = true
		if observation.GetSourceBlobId() == "" {
			continue
		}
		observations[observation.GetSourceBlobId()] = append(observations[observation.GetSourceBlobId()], observation)
	}

	sourceIDs := make([]string, 0, len(batch.Sources))
	seenSources := make(map[string]bool, len(batch.Sources))
	for _, source := range batch.Sources {
		id := source.GetMeta().GetRecordId()
		if seenSources[id] {
			return RegulationIdentityPlan{}, errors.New("duplicate source blob in identity input")
		}
		seenSources[id] = true
		sourceIDs = append(sourceIDs, id)
	}
	for sourceID := range observations {
		if !seenSources[sourceID] {
			return RegulationIdentityPlan{}, errors.New("source observation references a blob outside the document batch")
		}
	}
	sort.Strings(sourceIDs)

	plan := RegulationIdentityPlan{}
	for _, sourceID := range sourceIDs {
		candidate, problems := regulationCandidate(sourceID, observations[sourceID], policy)
		if len(problems) > 0 {
			plan.Reviews = append(plan.Reviews, regulationReview(batch.GetMeta().GetCorpusId(), sourceID, candidate, problems))
			continue
		}
		plan.Candidates = append(plan.Candidates, candidate)
	}
	return plan, nil
}

func regulationCandidate(sourceID string, observations []*pb.SourceObservation, policy RegulationIdentityPolicy) (RegulationIdentityCandidate, []string) {
	candidate := RegulationIdentityCandidate{SourceBlobID: sourceID, Jurisdiction: strings.TrimSpace(policy.Jurisdiction)}
	if len(observations) == 0 {
		return candidate, []string{"observation"}
	}
	values := map[string]map[string]string{}
	var problems []string
	for _, observation := range observations {
		candidate.ObservationIDs = append(candidate.ObservationIDs, observation.GetMeta().GetRecordId())
		if observation.GetStatus() != pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE {
			problems = append(problems, "observation_status:not_complete")
		}
		if observation.GetMetadataHash() == nil {
			problems = append(problems, "metadata_hash:missing")
		}
		for _, value := range observation.PortalMetadata {
			name := strings.TrimSpace(value.GetName())
			text := strings.TrimSpace(value.GetText())
			if name == "" || text == "" {
				continue
			}
			if values[name] == nil {
				values[name] = map[string]string{}
			}
			normalized := normalizeIdentityText(text)
			if previous, exists := values[name][normalized]; !exists || text < previous {
				values[name][normalized] = text
			}
		}
	}
	sort.Strings(candidate.ObservationIDs)
	candidate.ObservationIDs = uniqueStrings(candidate.ObservationIDs)

	candidate.Kind, problems = exactField(values, []string{"regulation_type", "document_type"}, "kind", problems)
	candidate.IssuerLabel, problems = exactField(values, []string{"issuer"}, "issuer", problems)
	candidate.OfficialNumber, problems = exactField(values, []string{"number"}, "official_number", problems)
	candidate.Title, problems = exactField(values, []string{"page_title", "title"}, "title", problems)
	if sourcedJurisdiction, exists, conflict := optionalExactField(values, []string{"jurisdiction"}); conflict {
		problems = append(problems, "jurisdiction:conflict")
	} else if exists && normalizeIdentityText(sourcedJurisdiction) != normalizeIdentityText(candidate.Jurisdiction) {
		problems = append(problems, "jurisdiction:policy_conflict")
	}
	yearText, nextProblems := exactField(values, []string{"year"}, "year", problems)
	problems = nextProblems
	if yearText != "" {
		year, err := strconv.ParseUint(strings.TrimSpace(yearText), 10, 32)
		if err != nil || year < 1000 || year > 9999 {
			problems = append(problems, "year:invalid")
		} else {
			candidate.Year = uint32(year)
		}
	}
	if len(problems) == 0 {
		candidate.IdentityKey = regulationIdentityKey(candidate)
	}
	return candidate, uniqueSorted(problems)
}

func optionalExactField(values map[string]map[string]string, aliases []string) (string, bool, bool) {
	canonical := map[string]string{}
	for _, alias := range aliases {
		for normalized, original := range values[alias] {
			if previous, exists := canonical[normalized]; !exists || original < previous {
				canonical[normalized] = original
			}
		}
	}
	if len(canonical) == 0 {
		return "", false, false
	}
	if len(canonical) > 1 {
		return "", true, true
	}
	for _, value := range canonical {
		return value, true, false
	}
	panic("unreachable optional exact field")
}

func exactField(values map[string]map[string]string, aliases []string, label string, problems []string) (string, []string) {
	canonical := map[string]string{}
	for _, alias := range aliases {
		for normalized, original := range values[alias] {
			if previous, exists := canonical[normalized]; !exists || original < previous {
				canonical[normalized] = original
			}
		}
	}
	if len(canonical) == 0 {
		return "", append(problems, label+":missing")
	}
	if len(canonical) > 1 {
		return "", append(problems, label+":conflict")
	}
	for _, value := range canonical {
		return strings.TrimSpace(value), problems
	}
	panic("unreachable exact field")
}

func regulationIdentityKey(candidate RegulationIdentityCandidate) string {
	payload := struct {
		Namespace      string `json:"namespace"`
		Kind           string `json:"kind"`
		Issuer         string `json:"issuer"`
		Jurisdiction   string `json:"jurisdiction"`
		OfficialNumber string `json:"official_number"`
		Year           uint32 `json:"year"`
	}{
		Namespace: RegulationIdentityKeyNamespace, Kind: normalizeIdentityText(candidate.Kind),
		Issuer: normalizeIdentityText(candidate.IssuerLabel), Jurisdiction: normalizeIdentityText(candidate.Jurisdiction),
		OfficialNumber: normalizeIdentityText(candidate.OfficialNumber), Year: candidate.Year,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func regulationReview(corpusID, sourceID string, candidate RegulationIdentityCandidate, problems []string) *pb.ReviewItem {
	evidence := append([]string(nil), candidate.ObservationIDs...)
	if len(evidence) == 0 {
		evidence = []string{sourceID}
	}
	values := []*pb.NamedValue{
		{Name: "source_blob_id", Value: &pb.NamedValue_Text{Text: sourceID}},
		{Name: "problems", Value: &pb.NamedValue_Text{Text: strings.Join(problems, ",")}},
	}
	for _, item := range []struct{ name, value string }{
		{"kind", candidate.Kind}, {"issuer", candidate.IssuerLabel}, {"jurisdiction", candidate.Jurisdiction},
		{"official_number", candidate.OfficialNumber}, {"year", strconv.FormatUint(uint64(candidate.Year), 10)}, {"title", candidate.Title},
	} {
		if item.value != "" && item.value != "0" {
			values = append(values, &pb.NamedValue{Name: item.name, Value: &pb.NamedValue_Text{Text: item.value}})
		}
	}
	key := corpusID + "\x00" + sourceID + "\x00" + strings.Join(problems, "\x00")
	sum := sha256.Sum256([]byte(key))
	return &pb.ReviewItem{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: "review:regulation:" + hex.EncodeToString(sum[:])},
		Kind: "regulation_identity", ProposedValues: values, EvidenceRefs: evidence,
		Status: pb.ReviewState_REVIEW_STATE_UNREVIEWED, Revision: 1,
	}
}

func normalizeIdentityText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return ' '
		}
		return unicode.ToLower(character)
	}, strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	return uniqueStrings(values)
}
