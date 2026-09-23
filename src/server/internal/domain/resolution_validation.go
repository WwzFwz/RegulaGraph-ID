// Validates the immutable RESOLVE result against its verified EXTRACT input before registration.
// The Go coordinator owns this trust boundary: every mention is accounted for, proposals retain
// source evidence, and canonical assignments name an explicit registry revision. This structural
// check does not prove registry state or whether a semantic link is legally correct; registry
// receipts and gold evaluation own those separate checks.
// Work is bounded by maximumReferenceEdges and the caller's validated wire-size limit. Required quality
// and latency targets remain REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.
package domain

import (
	"errors"
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ValidateResolutionBatchClosure checks cross-record identities and proposal/decision coverage.
// sourceRef must be the immutable artifact ref whose bytes produced source.
func ValidateResolutionBatchClosure(batch *pb.ResolutionBatch, source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef, maximumReferenceEdges int) error {
	if batch == nil || source == nil || sourceRef == nil || sourceRef.ArtifactId == "" || sourceRef.ContentHash == nil || maximumReferenceEdges <= 0 ||
		batch.Meta == nil || batch.Context == nil || batch.SourceExtractionBatch == nil ||
		batch.Dependencies == nil || batch.ModelManifest == nil || batch.ItemCounts == nil ||
		source.Meta == nil || source.Context == nil {
		return errors.New("resolution batch, extraction input, source ref, and positive limit are required")
	}
	if len(source.Mentions) > maximumReferenceEdges || len(source.Supports) > maximumReferenceEdges ||
		len(batch.Proposals) > maximumReferenceEdges || len(batch.Decisions) > maximumReferenceEdges ||
		len(batch.Issues) > maximumReferenceEdges {
		return errors.New("resolution record count exceeds the configured limit")
	}
	corpus := batch.Meta.CorpusId
	if corpus == "" || corpus != batch.Context.CorpusId || corpus != source.Meta.CorpusId ||
		corpus != source.Context.CorpusId || batch.Context.SchemaVersion != source.Context.SchemaVersion ||
		batch.Context.AuthScopeRef == "" || batch.Context.AuthScopeRef != source.Context.AuthScopeRef ||
		batch.Context.ConfigFingerprint == nil || !proto.Equal(batch.Context.ConfigFingerprint, source.Context.ConfigFingerprint) ||
		!proto.Equal(batch.Context.SnapshotRef, source.Context.SnapshotRef) || batch.OntologyVersion == "" ||
		batch.OntologyVersion != source.OntologyVersion || batch.RegistryRevision == 0 {
		return errors.New("resolution corpus, ontology, or registry revision differs from extraction input")
	}
	if !proto.Equal(batch.SourceExtractionBatch, sourceRef) {
		return errors.New("resolution source ref differs from verified extraction artifact")
	}
	if batch.ModelManifest.Task != pb.ModelTask_MODEL_TASK_RESOLVE ||
		batch.Dependencies.ProducerManifest == nil ||
		!containsModel(batch.Dependencies.ProducerManifest.Models, batch.ModelManifest) {
		return errors.New("resolution producer does not bind its RESOLVE model")
	}
	boundSource := false
	for _, dependency := range batch.Dependencies.Dependencies {
		if dependency != nil && dependency.DependencyId == sourceRef.ArtifactId &&
			proto.Equal(dependency.Fingerprint, sourceRef.ContentHash) {
			boundSource = true
		}
	}
	if !boundSource {
		return errors.New("resolution dependency manifest does not bind extraction input")
	}
	mentions := make(map[string]*pb.Mention, len(source.Mentions))
	sourceRefs := map[string]bool{}
	knownEvidenceIDs := map[string]bool{source.Meta.RecordId: true}
	spans := make([]*pb.TextSpan, 0, len(source.Mentions)+len(source.Supports))
	sourceWork := 0
	countSourceWork := func(amount int) error {
		if amount < 0 || sourceWork > maximumReferenceEdges-amount {
			return errors.New("resolution source reference work exceeds the configured limit")
		}
		sourceWork += amount
		return nil
	}
	for _, mention := range source.Mentions {
		if err := countSourceWork(1 + len(mention.GetSourceRefs())); err != nil {
			return err
		}
		id := mention.GetMeta().GetRecordId()
		if id == "" || mentions[id] != nil || mention.GetMeta().GetCorpusId() != corpus || mention.GetTextSpan() == nil {
			return errors.New("extraction input has duplicate or invalid mention identity")
		}
		mentions[id] = mention
		knownEvidenceIDs[id] = true
		spans = append(spans, mention.TextSpan)
		for _, reference := range mention.SourceRefs {
			if reference == nil || sourceRefKey(reference) == "\x00\x00" {
				return errors.New("extraction mention has invalid source version")
			}
			sourceRefs[sourceRefKey(reference)] = true
		}
	}
	for _, support := range source.Supports {
		if support == nil {
			return errors.New("extraction input has nil support")
		}
		if err := countSourceWork(len(support.SourceRefs) + len(support.EvidenceSpans)); err != nil {
			return err
		}
		spans = append(spans, support.EvidenceSpans...)
		if id := support.GetMeta().GetRecordId(); id != "" {
			knownEvidenceIDs[id] = true
		}
		for _, reference := range support.SourceRefs {
			if reference == nil || sourceRefKey(reference) == "\x00\x00" {
				return errors.New("extraction support has invalid source version")
			}
			sourceRefs[sourceRefKey(reference)] = true
		}
	}
	for _, assertion := range source.Assertions {
		if id := assertion.GetMeta().GetRecordId(); id != "" {
			knownEvidenceIDs[id] = true
		}
	}
	if batch.Meta.RecordId == "" || knownEvidenceIDs[batch.Meta.RecordId] {
		return errors.New("resolution batch record ID collides with extraction input")
	}
	knownEvidenceIDs[batch.Meta.RecordId] = true
	evidenceCoverage := buildEvidenceCoverage(spans)
	counts := batch.ItemCounts
	if counts.Expected != uint64(len(mentions)) || counts.Accepted > counts.Expected ||
		math.MaxUint64-counts.Accepted < counts.Rejected || counts.Accepted+counts.Rejected != counts.Expected {
		return errors.New("resolution item accounting differs from extraction mentions")
	}
	expectedCompleteness := pb.Completeness_COMPLETENESS_COMPLETE
	if counts.Rejected > 0 {
		expectedCompleteness = pb.Completeness_COMPLETENESS_PARTIAL
		if counts.Accepted == 0 {
			expectedCompleteness = pb.Completeness_COMPLETENESS_NONE
		}
	}
	if batch.Completeness != expectedCompleteness {
		return errors.New("resolution completeness differs from item accounting")
	}
	proposals := make(map[string]*pb.ResolutionProposal, len(batch.Proposals))
	covered := make(map[string]bool, len(mentions))
	work := 0
	addWork := func(count int) error {
		if count < 0 || work > maximumReferenceEdges-count {
			return errors.New("resolution reference work exceeds the configured limit")
		}
		work += count
		return nil
	}
	for _, proposal := range batch.Proposals {
		id := proposal.GetMeta().GetRecordId()
		if id == "" || proposal.GetMeta().GetCorpusId() != corpus || knownEvidenceIDs[id] ||
			proposal.ExpectedRegistryRevision == 0 || proposal.ExpectedRegistryRevision > batch.RegistryRevision {
			return errors.New("resolution proposal identity or registry revision is invalid")
		}
		if len(proposal.MentionIds) == 0 || proposal.Evidence == nil || len(proposal.Evidence.Locators) != 0 ||
			len(proposal.Evidence.Sources) == 0 || len(proposal.Evidence.Spans) == 0 {
			return fmt.Errorf("resolution proposal %q lacks mentions or source evidence", id)
		}
		if err := addWork(len(proposal.MentionIds) + len(proposal.CandidateIds) + len(proposal.Evidence.Sources) + len(proposal.Evidence.Spans)); err != nil {
			return err
		}
		evidenceSources := make(map[string]bool, len(proposal.Evidence.Sources))
		for _, reference := range proposal.Evidence.Sources {
			if reference == nil || !sourceRefs[sourceRefKey(reference)] {
				return fmt.Errorf("resolution proposal %q invents source-version evidence", id)
			}
			evidenceSources[sourceRefKey(reference)] = true
		}
		for _, span := range proposal.Evidence.Spans {
			if span == nil || span.StartByte >= span.EndByte || !evidenceCoverage.contains(span) {
				return fmt.Errorf("resolution proposal %q invents text evidence", id)
			}
		}
		proposalCoverage := buildEvidenceCoverage(proposal.Evidence.Spans)
		for _, mentionID := range proposal.MentionIds {
			mention := mentions[mentionID]
			if mention == nil || covered[mentionID] {
				return fmt.Errorf("resolution proposal %q repeats or invents mention %q", id, mentionID)
			}
			hasSource := false
			for _, original := range mention.SourceRefs {
				hasSource = hasSource || evidenceSources[sourceRefKey(original)]
			}
			hasSpan := proposalCoverage.contains(mention.TextSpan)
			if !hasSource || !hasSpan {
				return fmt.Errorf("resolution proposal %q lacks evidence for mention %q", id, mentionID)
			}
			covered[mentionID] = true
		}
		if proposal.Action == pb.ResolutionAction_RESOLUTION_ACTION_LINK && len(proposal.CandidateIds) != 1 ||
			proposal.Action == pb.ResolutionAction_RESOLUTION_ACTION_CREATE && (len(proposal.CandidateIds) != 0 || len(proposal.ProposedIdentityKeys) == 0) {
			return fmt.Errorf("resolution proposal %q has invalid action targets", id)
		}
		proposals[id] = proposal
		knownEvidenceIDs[id] = true
	}
	if uint64(len(covered)) != counts.Accepted || len(batch.Decisions) != len(proposals) {
		return errors.New("resolution proposals, decisions, and accepted item count disagree")
	}
	decided := map[string]bool{}
	decisionIDs := map[string]bool{}
	for _, decision := range batch.Decisions {
		proposal := proposals[decision.GetProposalId()]
		if proposal == nil || decided[decision.ProposalId] || decision.GetMeta().GetCorpusId() != corpus ||
			decision.GetMeta().GetRecordId() == "" || knownEvidenceIDs[decision.GetMeta().GetRecordId()] || decision.RegistryRevision == 0 ||
			decision.RegistryRevision > batch.RegistryRevision || decision.RegistryRevision < proposal.ExpectedRegistryRevision ||
			decision.Action != proposal.Action {
			return errors.New("resolution decision has stale or unknown proposal binding")
		}
		decided[decision.ProposalId] = true
		decisionIDs[decision.GetMeta().GetRecordId()] = true
		knownEvidenceIDs[decision.GetMeta().GetRecordId()] = true
		switch decision.Action {
		case pb.ResolutionAction_RESOLUTION_ACTION_LINK:
			if len(decision.AssignedCanonicalIds) != 1 || decision.AssignedCanonicalIds[0] != proposal.CandidateIds[0] {
				return errors.New("resolution LINK decision did not assign its proposed candidate")
			}
		case pb.ResolutionAction_RESOLUTION_ACTION_CREATE:
			if len(decision.AssignedCanonicalIds) != 1 {
				return errors.New("resolution CREATE decision requires one new canonical ID")
			}
		case pb.ResolutionAction_RESOLUTION_ACTION_DEFER, pb.ResolutionAction_RESOLUTION_ACTION_REJECT:
			if len(decision.AssignedCanonicalIds) != 0 {
				return errors.New("resolution nonassignment decision contains a canonical ID")
			}
		case pb.ResolutionAction_RESOLUTION_ACTION_MERGE, pb.ResolutionAction_RESOLUTION_ACTION_SPLIT:
			if len(decision.AssignedCanonicalIds) == 0 {
				return errors.New("resolution merge/split decision requires registry assignments")
			}
		default:
			return errors.New("resolution decision action is unspecified")
		}
	}
	rejected := map[string]bool{}
	for _, issue := range batch.Issues {
		if issue == nil || (mentions[issue.RecordId] == nil && proposals[issue.RecordId] == nil) {
			return errors.New("resolution issue references an unknown mention or proposal")
		}
		if err := addWork(len(issue.EvidenceRefs)); err != nil {
			return err
		}
		for _, evidenceID := range issue.EvidenceRefs {
			if !knownEvidenceIDs[evidenceID] {
				return fmt.Errorf("resolution issue references unknown evidence %q", evidenceID)
			}
		}
		if issue.Severity == pb.Severity_SEVERITY_ERROR {
			if mentions[issue.RecordId] == nil || covered[issue.RecordId] {
				return errors.New("resolution error issue must identify a rejected mention")
			}
			rejected[issue.RecordId] = true
		}
	}
	if uint64(len(rejected)) != counts.Rejected {
		return errors.New("resolution rejected count differs from distinct failed mentions")
	}
	for id := range mentions {
		if !covered[id] && !rejected[id] {
			return errors.New("resolution silently dropped an extraction mention")
		}
	}
	return nil
}
