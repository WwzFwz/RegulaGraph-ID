// Validates a registry RESOLVE response against the exact request and pinned candidate artifact.
// The coordinator calls this only after receiving a response from its trusted registry adapter;
// this structural check cannot authenticate a remote sender or prove legal identity correctness.
// Work is bounded by the caller's reference/candidate limits and the shared wire byte budget;
// measure p95/p99 and resolution accuracy against configs/benchmark-targets.yaml (unmeasured).
package domain

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ValidateRegistryResolveReceipt requires an atomic, successful LINK/DEFER decision for each
// proposal. Error or partial responses must be handled as failed operations, never published.
func ValidateRegistryResolveReceipt(source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef,
	candidates *pb.RegistryCandidateBatch, request *pb.RegistryResolveRequest,
	response *pb.RegistryResolveResponse, maximumReferences, maximumCandidatesPerMention int) error {
	if request == nil || response == nil || request.Context == nil || candidates == nil ||
		candidates.Context == nil || source == nil || source.Context == nil ||
		maximumReferences <= 0 || maximumCandidatesPerMention <= 0 ||
		proto.Size(request) > DefaultWireLimits.MaxBytes || proto.Size(response) > DefaultWireLimits.MaxBytes {
		return errors.New("bounded registry request, response, and source artifacts are required")
	}
	if err := ValidateWire(request, DefaultWireLimits); err != nil {
		return fmt.Errorf("invalid registry request wire: %w", err)
	}
	if err := ValidateWire(response, DefaultWireLimits); err != nil {
		return fmt.Errorf("invalid registry response wire: %w", err)
	}
	if request.ExpectedRevision == 0 || request.ExpectedRevision != candidates.RegistryRevision ||
		request.Context.SchemaVersion != source.Context.SchemaVersion ||
		request.Context.CorpusId != source.Context.CorpusId ||
		request.Context.AuthScopeRef != source.Context.AuthScopeRef ||
		!proto.Equal(request.Context.ConfigFingerprint, source.Context.ConfigFingerprint) ||
		!proto.Equal(request.Context.SnapshotRef, source.Context.SnapshotRef) {
		return errors.New("registry request differs from pinned extraction context or revision")
	}
	if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		request.Proposals, maximumReferences, maximumCandidatesPerMention); err != nil {
		return fmt.Errorf("registry request proposals are invalid: %w", err)
	}
	if response.RequestId != request.Context.RequestId ||
		response.RegistryRevision != request.ExpectedRevision ||
		len(response.Assignments) != len(request.Proposals) {
		return errors.New("registry receipt request, revision, or assignment count differs")
	}
	proposals := make(map[string]*pb.ResolutionProposal, len(request.Proposals))
	correlations := make(map[string]bool, len(request.Proposals))
	reservedIDs := make(map[string]bool, len(request.Proposals)+len(source.Mentions)+
		len(source.Assertions)+len(source.Supports)+len(candidates.Candidates)+len(candidates.Aliases)+4)
	for _, id := range []string{source.Meta.RecordId, sourceRef.ArtifactId, candidates.Meta.RecordId} {
		reservedIDs[id] = true
	}
	for _, mention := range source.Mentions {
		reservedIDs[mention.GetMeta().GetRecordId()] = true
	}
	for _, assertion := range source.Assertions {
		reservedIDs[assertion.GetMeta().GetRecordId()] = true
	}
	for _, support := range source.Supports {
		reservedIDs[support.GetMeta().GetRecordId()] = true
	}
	for _, candidate := range candidates.Candidates {
		reservedIDs[candidate.GetMeta().GetRecordId()] = true
	}
	for _, alias := range candidates.Aliases {
		reservedIDs[alias.GetMeta().GetRecordId()] = true
	}
	for _, proposal := range request.Proposals {
		if correlations[proposal.LocalCorrelationId] {
			return errors.New("duplicate proposal correlation ID")
		}
		correlations[proposal.LocalCorrelationId] = true
		proposals[proposal.Meta.RecordId] = proposal
		reservedIDs[proposal.Meta.RecordId] = true
	}
	seen := make(map[string]bool, len(response.Assignments))
	decisionIDs := make(map[string]bool, len(response.Assignments))
	for _, assignment := range response.Assignments {
		if assignment == nil {
			return errors.New("nil registry assignment")
		}
		proposal := proposals[assignment.ProposalId]
		decision := assignment.GetDecision()
		if proposal == nil || seen[assignment.ProposalId] ||
			assignment.LocalCorrelationId != proposal.LocalCorrelationId || decision == nil ||
			assignment.GetError() != nil {
			return fmt.Errorf("registry assignment %q is missing, repeated, failed, or mismatched", assignment.ProposalId)
		}
		seen[assignment.ProposalId] = true
		if decision.Meta == nil || decision.Meta.RecordId == "" ||
			reservedIDs[decision.Meta.RecordId] || decisionIDs[decision.Meta.RecordId] ||
			decision.Meta.CorpusId != source.Meta.CorpusId ||
			decision.Meta.SchemaVersion != source.Meta.SchemaVersion ||
			decision.Meta.Visibility != nil || decision.ProposalId != assignment.ProposalId ||
			decision.RegistryRevision != response.RegistryRevision ||
			decision.Action != proposal.Action || decision.Supersedes != nil ||
			decision.Actor == "" || decision.Reason == "" {
			return fmt.Errorf("registry decision for %q breaks provenance or revision binding", assignment.ProposalId)
		}
		decisionIDs[decision.Meta.RecordId] = true
		switch proposal.Action {
		case pb.ResolutionAction_RESOLUTION_ACTION_LINK:
			if len(decision.AssignedCanonicalIds) != 1 ||
				decision.AssignedCanonicalIds[0] != proposal.CandidateIds[0] {
				return fmt.Errorf("registry LINK for %q changed the pinned candidate", assignment.ProposalId)
			}
		case pb.ResolutionAction_RESOLUTION_ACTION_DEFER:
			if len(decision.AssignedCanonicalIds) != 0 {
				return fmt.Errorf("registry DEFER for %q assigned a canonical ID", assignment.ProposalId)
			}
		default:
			return fmt.Errorf("unsupported registry receipt action %s", proposal.Action)
		}
	}
	return nil
}
