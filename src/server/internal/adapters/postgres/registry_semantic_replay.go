// Reconstructs an immutable semantic registry receipt from PostgreSQL operation history.
// Replays verify stored hash, row count, revision, correlation, action, canonical target, and
// protobuf payload before returning any assignment. This is a local storage integrity check,
// not proof that a LINK is legally correct. Measure replay p95/p99 and corruption detection;
// numeric targets remain REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func loadSemanticOperation(ctx context.Context, tx pgx.Tx, request *pb.RegistryResolveRequest,
	requestHash string, current int64, approvals map[string]ReviewedLink) (*pb.RegistryResolveResponse, bool, error) {
	corpusID := request.Context.CorpusId
	var storedHash string
	var expected, committed int64
	var count int
	err := tx.QueryRow(ctx, `SELECT request_hash,expected_revision,committed_revision,decision_count
		FROM registry_semantic_operations WHERE corpus_id=$1 AND operation_key=$2`, corpusID,
		request.OperationKey).Scan(&storedHash, &expected, &committed, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read semantic operation: %w", err)
	}
	if storedHash != requestHash || expected != int64(request.ExpectedRevision) ||
		count != len(request.Proposals) {
		return nil, false, fmt.Errorf("semantic operation key reused with different content: %w", ErrConflict)
	}
	if committed != expected+1 || committed > current || committed <= 0 {
		return nil, false, fmt.Errorf("semantic operation revision is corrupt: %w", domain.ErrPersistentIntegrity)
	}
	rows, err := tx.Query(ctx, `SELECT proposal_id,correlation_id,decision_id,action,canonical_id,review_id,
		registry_revision,decision_payload,decision_hash FROM registry_semantic_decisions
		WHERE corpus_id=$1 AND operation_key=$2`, corpusID, request.OperationKey)
	if err != nil {
		return nil, false, fmt.Errorf("read semantic decision rows: %w", err)
	}
	defer rows.Close()
	assignments := make(map[string]*pb.RegistryAssignment, count)
	for rows.Next() {
		var proposalID, correlationID, decisionID, storedDecisionHash string
		var action int16
		var canonicalID, reviewID *string
		var revision int64
		var raw []byte
		if err = rows.Scan(&proposalID, &correlationID, &decisionID, &action, &canonicalID, &reviewID,
			&revision, &raw, &storedDecisionHash); err != nil {
			return nil, false, fmt.Errorf("scan semantic decision: %w", err)
		}
		if len(assignments) >= count || assignments[proposalID] != nil || revision != committed {
			return nil, false, fmt.Errorf("semantic decision count or revision differs: %w", domain.ErrPersistentIntegrity)
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != storedDecisionHash {
			return nil, false, fmt.Errorf("semantic decision payload hash differs: %w", domain.ErrPersistentIntegrity)
		}
		decision := new(pb.ResolutionDecision)
		if err = proto.Unmarshal(raw, decision); err != nil {
			return nil, false, fmt.Errorf("decode semantic decision: %w", domain.ErrPersistentIntegrity)
		}
		if err = domain.ValidateWire(decision, domain.DefaultWireLimits); err != nil {
			return nil, false, fmt.Errorf("invalid stored semantic decision: %w", domain.ErrPersistentIntegrity)
		}
		if decision.GetMeta().GetRecordId() != decisionID || decision.ProposalId != proposalID ||
			decision.Action != pb.ResolutionAction(action) || decision.RegistryRevision != uint64(revision) ||
			len(decision.AssignedCanonicalIds) != boolToInt(canonicalID != nil) ||
			canonicalID != nil && decision.AssignedCanonicalIds[0] != *canonicalID {
			return nil, false, fmt.Errorf("semantic decision columns differ from payload: %w", domain.ErrPersistentIntegrity)
		}
		approval, approved := approvals[proposalID]
		if (action == int16(pb.ResolutionAction_RESOLUTION_ACTION_LINK) &&
			(!approved || reviewID == nil || *reviewID != approval.ReviewID ||
				decision.Actor != approval.Actor ||
				decision.Reason != approval.Reason+" [review:"+approval.ReviewID+"]")) ||
			(action == int16(pb.ResolutionAction_RESOLUTION_ACTION_DEFER) &&
				(reviewID != nil || approved || decision.Actor != "registry-policy" ||
					decision.Reason != "identity remains unresolved")) {
			return nil, false, fmt.Errorf("semantic review or policy differs from stored decision: %w", domain.ErrPersistentIntegrity)
		}
		assignments[proposalID] = &pb.RegistryAssignment{ProposalId: proposalID,
			LocalCorrelationId: correlationID,
			Result:             &pb.RegistryAssignment_Decision{Decision: decision}}
	}
	if err = rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read semantic decision rows: %w", err)
	}
	if len(assignments) != count {
		return nil, false, fmt.Errorf("semantic operation has missing decisions: %w", domain.ErrPersistentIntegrity)
	}
	response := &pb.RegistryResolveResponse{RequestId: request.Context.RequestId,
		RegistryRevision: uint64(committed)}
	for _, proposal := range request.Proposals {
		assignment := assignments[proposal.GetMeta().GetRecordId()]
		if assignment == nil || assignment.LocalCorrelationId != proposal.LocalCorrelationId {
			return nil, false, fmt.Errorf("stored semantic assignment differs from proposal: %w", domain.ErrPersistentIntegrity)
		}
		if !proto.Equal(assignment.GetDecision(), semanticDecision(corpusID, request.OperationKey,
			proposal, approvals[proposal.Meta.RecordId], uint64(committed))) {
			return nil, false, fmt.Errorf("stored semantic decision differs from deterministic receipt: %w",
				domain.ErrPersistentIntegrity)
		}
		response.Assignments = append(response.Assignments, assignment)
	}
	return response, true, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
