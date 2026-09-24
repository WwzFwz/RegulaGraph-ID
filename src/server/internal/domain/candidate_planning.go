// Builds exact, revision-observable alias lookup plans from verified EXTRACT mentions.
// Corpus policy supplies the legal scopes for each entity type; source regulation IDs add
// document-local scopes only for configured types. Every generated scope is retained, including
// negative lookups, and overflow fails explicitly. This planner never decides identity or
// authorizes LINK. Compare candidate recall, false exclusions, plan bytes and p95/p99 against
// configs/benchmark-targets.yaml; required targets remain REQUIRED_UNMEASURED.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// CandidatePlanningPolicy is pinned by the caller to one corpus configuration. Scope values
// must match registry canonical scopes exactly; they are legal namespace choices, not inferred
// from a portal URL. A type absent from this policy is an error, never an empty lookup.
type CandidatePlanningPolicy struct {
	ScopesByType                map[string][]string
	IncludeSourceRegulationType map[string]bool
	MaximumMentions             int
	MaximumScopesPerMention     int
	MaximumTotalScopes          int
}

// Fingerprint pins all legal scope choices and resource limits in the producer manifest.
// Go's JSON encoder sorts map keys, so equivalent policy maps have identical hashes.
func (policy CandidatePlanningPolicy) Fingerprint() (*pb.ContentHash, error) {
	if policy.MaximumMentions <= 0 || policy.MaximumScopesPerMention <= 0 ||
		policy.MaximumTotalScopes <= 0 || len(policy.ScopesByType) == 0 {
		return nil, errors.New("candidate policy requires positive limits and legal scopes")
	}
	for entityType, scopes := range policy.ScopesByType {
		if CanonicalEntityTypeCode(entityType) == 0 || len(scopes) == 0 {
			return nil, fmt.Errorf("unsupported or empty candidate policy type %q", entityType)
		}
		seen := map[string]bool{}
		for _, scope := range scopes {
			if !validCandidatePlanningScope(scope) || seen[scope] {
				return nil, fmt.Errorf("invalid or repeated scope for type %q", entityType)
			}
			seen[scope] = true
		}
	}
	for entityType, enabled := range policy.IncludeSourceRegulationType {
		if enabled && len(policy.ScopesByType[entityType]) == 0 {
			return nil, fmt.Errorf("source regulation scope lacks type policy %q", entityType)
		}
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(append([]byte("candidate-planning-policy:v1\x00"), raw...))
	return &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])}, nil
}

// PlanRegistryCandidates covers every EXTRACT mention with one or more exact lookup scopes.
// It does not read PostgreSQL: PrepareRegistryCandidateBatch obtains all positive and negative
// observations at one revision after this deterministic plan is built.
func PlanRegistryCandidates(source *pb.ExtractionBatch, policy CandidatePlanningPolicy) ([]RegistryCandidatePlan, error) {
	if _, err := policy.Fingerprint(); err != nil {
		return nil, err
	}
	if source == nil || source.Meta == nil || source.Context == nil ||
		source.Meta.CorpusId == "" || source.Meta.CorpusId != source.Context.CorpusId ||
		source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		policy.MaximumMentions <= 0 || policy.MaximumScopesPerMention <= 0 || policy.MaximumTotalScopes <= 0 ||
		len(source.Mentions) > policy.MaximumMentions {
		return nil, errors.New("complete extraction and positive candidate plan limits required")
	}
	if len(source.Mentions) == 0 {
		return nil, nil
	}
	plans := make([]RegistryCandidatePlan, 0, len(source.Mentions))
	seenMentions := make(map[string]bool, len(source.Mentions))
	total := 0
	for _, mention := range source.Mentions {
		if mention == nil || mention.Meta == nil || mention.Meta.CorpusId != source.Meta.CorpusId ||
			mention.Meta.RecordId == "" || seenMentions[mention.Meta.RecordId] ||
			mention.CandidateType == "" || CanonicalEntityTypeCode(mention.CandidateType) == 0 ||
			len(mention.SourceRefs) == 0 {
			return nil, errors.New("candidate plan mention has invalid identity, type, or provenance")
		}
		seenMentions[mention.Meta.RecordId] = true
		normalized, err := NormalizeCandidateSurface(mention.SurfaceForm)
		if err != nil {
			return nil, fmt.Errorf("normalize mention %q: %w", mention.Meta.RecordId, err)
		}
		configured, ok := policy.ScopesByType[mention.CandidateType]
		if !ok || len(configured) == 0 {
			return nil, fmt.Errorf("no pinned legal scopes for mention type %q", mention.CandidateType)
		}
		scopes := make(map[string]bool, len(configured)+len(mention.SourceRefs))
		for _, scope := range configured {
			if !validCandidatePlanningScope(scope) || scopes[scope] {
				return nil, fmt.Errorf("invalid or duplicate legal scope for type %q", mention.CandidateType)
			}
			scopes[scope] = true
		}
		for _, sourceRef := range mention.SourceRefs {
			if sourceRef == nil || sourceRef.SourceBlobId == "" || sourceRef.RegulationId == "" ||
				sourceRef.ProvisionVersionId == "" {
				return nil, fmt.Errorf("mention %q has incomplete source identity", mention.Meta.RecordId)
			}
			if policy.IncludeSourceRegulationType[mention.CandidateType] {
				if !validCandidatePlanningScope(sourceRef.RegulationId) {
					return nil, fmt.Errorf("mention %q has invalid source regulation scope", mention.Meta.RecordId)
				}
				scopes[sourceRef.RegulationId] = true
			}
		}
		if len(scopes) > policy.MaximumScopesPerMention || len(scopes) > policy.MaximumTotalScopes-total {
			return nil, errors.New("candidate scope budget exceeded; no lookup was truncated")
		}
		total += len(scopes)
		ordered := make([]string, 0, len(scopes))
		for scope := range scopes {
			ordered = append(ordered, scope)
		}
		sort.Strings(ordered)
		plan := RegistryCandidatePlan{MentionID: mention.Meta.RecordId, Scopes: make([]RegistryLookupScope, 0, len(ordered))}
		for _, scope := range ordered {
			plan.Scopes = append(plan.Scopes, RegistryLookupScope{EntityType: mention.CandidateType,
				CanonicalScope: scope, NormalizedLookup: normalized})
		}
		plans = append(plans, plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].MentionID < plans[j].MentionID })
	return plans, nil
}

// NormalizeCandidateSurface mirrors Rust LookupKey: Unicode whitespace collapse followed by
// full Unicode lowercase of the complete string. Contextual casing matters for final sigma.
// It does not erase legal numbers, punctuation, negation, or accents.
func NormalizeCandidateSurface(surface string) (string, error) {
	if !utf8.ValidString(surface) {
		return "", errors.New("candidate surface must be valid UTF-8")
	}
	compact := strings.Join(strings.Fields(surface), " ")
	if compact == "" {
		return "", errors.New("candidate surface is empty")
	}
	normalized := cases.Lower(language.Und).String(compact)
	if len(normalized) > 512 || strings.ContainsRune(normalized, '\x00') {
		return "", errors.New("normalized candidate surface exceeds registry contract")
	}
	return normalized, nil
}

func validCandidatePlanningScope(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && utf8.ValidString(value) &&
		len(value) <= 512 && !strings.ContainsRune(value, '\x00')
}
