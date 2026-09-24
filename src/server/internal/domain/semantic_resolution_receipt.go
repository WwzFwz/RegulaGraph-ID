// Builds the deterministic prospective LINK/DEFER receipt shared by PostgreSQL and Go workflow.
// The workflow uses it to validate the complete output before registry CAS; PostgreSQL uses the
// same function for durable decisions. This shape check does not authenticate review records or
// authorize the job fence. Work is O(proposals) with bounded wire size; benchmark quality and
// p95/p99 remain REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// PreviewSemanticResolutionReceipt returns exactly the decision shape the registry will write
// for a new operation at expected_revision+1. The registry still checks CAS/replay and reviews.
func PreviewSemanticResolutionReceipt(request *pb.RegistryResolveRequest,
	approvals []ReviewedLink) (*pb.RegistryResolveResponse, error) {
	if request == nil || request.Context == nil || request.OperationKey == "" ||
		request.ExpectedRevision == 0 || request.ExpectedRevision >= math.MaxInt64 ||
		len(request.Proposals) == 0 {
		return nil, errors.New("bounded semantic registry request is required")
	}
	byID := make(map[string]ReviewedLink, len(approvals))
	for _, approval := range approvals {
		if approval.ProposalID == "" || approval.CanonicalID == "" || approval.Actor == "" ||
			approval.ReviewID == "" || approval.Reason == "" || len(approval.Reason) > 1024 ||
			byID[approval.ProposalID].ProposalID != "" {
			return nil, errors.New("invalid or duplicate semantic approval")
		}
		byID[approval.ProposalID] = approval
	}
	response := &pb.RegistryResolveResponse{RequestId: request.Context.RequestId,
		RegistryRevision: request.ExpectedRevision + 1}
	linkCount := 0
	for _, proposal := range request.Proposals {
		if proposal == nil || proposal.Meta == nil || proposal.Meta.RecordId == "" ||
			proposal.Action != pb.ResolutionAction_RESOLUTION_ACTION_LINK &&
				proposal.Action != pb.ResolutionAction_RESOLUTION_ACTION_DEFER {
			return nil, errors.New("unsupported or invalid semantic proposal")
		}
		approval, approved := byID[proposal.Meta.RecordId]
		if proposal.Action == pb.ResolutionAction_RESOLUTION_ACTION_LINK {
			linkCount++
			if !approved || len(proposal.CandidateIds) != 1 ||
				approval.CanonicalID != proposal.CandidateIds[0] {
				return nil, fmt.Errorf("LINK lacks exact approval for %q", proposal.Meta.RecordId)
			}
		} else if approved {
			return nil, fmt.Errorf("DEFER carries LINK approval for %q", proposal.Meta.RecordId)
		}
		digest := sha256.Sum256([]byte(request.Context.CorpusId + "\x00" + request.OperationKey +
			"\x00" + proposal.Meta.RecordId))
		decision := &pb.ResolutionDecision{Meta: &pb.RecordMeta{
			SchemaVersion: proposal.Meta.SchemaVersion, CorpusId: request.Context.CorpusId,
			RecordId: "decision:semantic:" + hex.EncodeToString(digest[:])},
			ProposalId: proposal.Meta.RecordId, Action: proposal.Action,
			RegistryRevision: response.RegistryRevision}
		if approved {
			decision.AssignedCanonicalIds = []string{proposal.CandidateIds[0]}
			decision.Actor = approval.Actor
			decision.Reason = approval.Reason + " [review:" + approval.ReviewID + "]"
		} else {
			decision.Actor = "registry-policy"
			decision.Reason = "identity remains unresolved"
		}
		response.Assignments = append(response.Assignments, &pb.RegistryAssignment{
			ProposalId: proposal.Meta.RecordId, LocalCorrelationId: proposal.LocalCorrelationId,
			Result: &pb.RegistryAssignment_Decision{Decision: decision}})
	}
	if linkCount != len(byID) {
		return nil, errors.New("approval references an unknown proposal")
	}
	return response, nil
}
