// Commits reviewed LINK and explicit DEFER decisions in one revisioned PostgreSQL transaction.
// The adapter re-reads every positive and negative lookup before CAS, persists an immutable
// operation/decision ledger, and checks replay rows. A model proposal alone cannot authorize
// LINK: the trusted caller must supply a matching review approval and authenticate that actor.
// Measure lookup/lock/write p50/p95/p99, contention, and false LINK on gold; benchmark targets
// in configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// These aliases retain the adapter API while the shared boundary types live in domain.
type ReviewedLink = domain.ReviewedLink
type SemanticRegistryInputs = domain.SemanticRegistryInputs
type SemanticJobFence = domain.SemanticJobFence

// CommitSemanticResolutions verifies the claimed RESOLVE lease and EXTRACT checkpoint in
// the same transaction as the registry CAS. It writes all decisions or none and returns
// the same decisions on a replay while the claimed lease remains valid.
func (r *Repository) CommitSemanticResolutions(ctx context.Context,
	proof SemanticJobFence, input SemanticRegistryInputs, request *pb.RegistryResolveRequest,
	approvals []ReviewedLink, maximumReferences, maximumCandidatesPerMention int) (*pb.RegistryResolveResponse, error) {
	if !storageIDPattern.MatchString(proof.JobID) || !storageIDPattern.MatchString(proof.OwnerID) ||
		!storageIDPattern.MatchString(proof.SourceCheckpointID) || proof.Fence == 0 || proof.Fence > math.MaxInt64 {
		return nil, errors.New("valid RESOLVE job fence and EXTRACT checkpoint are required")
	}
	return r.commitSemanticResolutions(ctx, &proof, input, request, approvals,
		maximumReferences, maximumCandidatesPerMention)
}

// The unfenced core is private; fixture tests use it to isolate registry invariants.
func (r *Repository) commitSemanticResolutions(ctx context.Context, proof *SemanticJobFence,
	input SemanticRegistryInputs, request *pb.RegistryResolveRequest, approvals []ReviewedLink,
	maximumReferences, maximumCandidatesPerMention int) (*pb.RegistryResolveResponse, error) {
	if r == nil || r.pool == nil || ctx == nil || request == nil || request.Context == nil ||
		!storageIDPattern.MatchString(request.OperationKey) ||
		request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64-1 ||
		len(request.Proposals) == 0 || len(request.Proposals) > maximumRegistryClaims ||
		maximumReferences <= 0 || maximumCandidatesPerMention <= 0 {
		return nil, errors.New("bounded registry operation and pinned candidate revision are required")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid registry request: %w", err)
	}
	source, candidates, err := r.decodeSemanticInputs(ctx, request.Context.CorpusId, input)
	if err != nil {
		return nil, err
	}
	sourceRef := input.SourceRef
	if request.ExpectedRevision != candidates.RegistryRevision {
		return nil, errors.New("candidate registry revision differs from request")
	}
	if err := domain.ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		request.Proposals, maximumReferences, maximumCandidatesPerMention); err != nil {
		return nil, fmt.Errorf("invalid registry proposals: %w", err)
	}
	if request.Context.SchemaVersion != source.Context.SchemaVersion ||
		request.Context.CorpusId != source.Context.CorpusId ||
		request.Context.AuthScopeRef != source.Context.AuthScopeRef ||
		!proto.Equal(request.Context.ConfigFingerprint, source.Context.ConfigFingerprint) ||
		!proto.Equal(request.Context.SnapshotRef, source.Context.SnapshotRef) {
		return nil, errors.New("registry request context differs from extraction")
	}
	approvalByID, err := checkReviewedLinks(request.Proposals, approvals)
	if err != nil {
		return nil, err
	}
	requestHash, err := semanticRequestHash(request, candidates, approvals)
	if err != nil {
		return nil, err
	}
	corpusID := request.Context.CorpusId
	// A replay may arrive after other registry writes; skip current lookup only when its
	// immutable operation header is already present. The locked transaction checks it again.
	var previouslyCommitted bool
	if err = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM registry_semantic_operations
		WHERE corpus_id=$1 AND operation_key=$2)`, corpusID, request.OperationKey).Scan(&previouslyCommitted); err != nil {
		return nil, fmt.Errorf("inspect semantic operation: %w", err)
	}
	if !previouslyCommitted {
		if err = r.verifySemanticCandidateRead(ctx, source, sourceRef, candidates,
			maximumReferences, maximumCandidatesPerMention); err != nil {
			return nil, err
		}
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin semantic registry operation: %w", err)
	}
	defer tx.Rollback(ctx)
	var current int64
	if err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`,
		corpusID).Scan(&current); err != nil {
		return nil, fmt.Errorf("lock semantic registry revision: %w", err)
	}
	if proof != nil {
		if err = verifySemanticJobFence(ctx, tx, *proof, corpusID, sourceRef, source); err != nil {
			return nil, err
		}
	}
	response, found, err := loadSemanticOperation(ctx, tx, request, requestHash, current, approvalByID)
	if err != nil {
		return nil, err
	}
	if found {
		if err = domain.ValidateRegistryResolveReceipt(source, sourceRef, candidates, request,
			response, maximumReferences, maximumCandidatesPerMention); err != nil {
			return nil, fmt.Errorf("stored registry receipt is invalid: %w", domain.ErrPersistentIntegrity)
		}
		if proof != nil {
			if err = verifySemanticLeaseStillLive(ctx, tx, *proof); err != nil {
				return nil, err
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("complete semantic replay: %w", err)
		}
		return response, nil
	}
	if current != int64(request.ExpectedRevision) {
		return nil, fmt.Errorf("semantic registry revision changed: %w", ErrConflict)
	}
	proposalIDs := make([]string, 0, len(request.Proposals))
	for _, proposal := range request.Proposals {
		proposalIDs = append(proposalIDs, proposal.Meta.RecordId)
	}
	var alreadyDecided bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM registry_semantic_decisions
		WHERE corpus_id=$1 AND proposal_id=ANY($2))`, corpusID, proposalIDs).Scan(&alreadyDecided); err != nil {
		return nil, fmt.Errorf("inspect prior semantic decisions: %w", err)
	}
	if alreadyDecided {
		return nil, fmt.Errorf("semantic proposal already has a decision: %w", ErrConflict)
	}
	if err = verifyStoredSemanticReviews(ctx, tx, corpusID, sourceRef.ArtifactId,
		input.CandidateRef, request.Proposals, approvalByID); err != nil {
		return nil, err
	}
	revision := current + 1
	response, err = domain.PreviewSemanticResolutionReceipt(request, approvals)
	if err != nil {
		return nil, fmt.Errorf("build semantic registry receipt: %w", err)
	}
	if response.RegistryRevision != uint64(revision) {
		return nil, errors.New("semantic receipt revision differs from registry CAS")
	}
	if err = domain.ValidateRegistryResolveReceipt(source, sourceRef, candidates, request,
		response, maximumReferences, maximumCandidatesPerMention); err != nil {
		return nil, fmt.Errorf("new registry receipt is invalid: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE corpus_state SET registry_revision=$2,updated_at=clock_timestamp()
		WHERE corpus_id=$1`, corpusID, revision); err != nil {
		return nil, fmt.Errorf("advance semantic registry revision: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO registry_semantic_operations
		(corpus_id,operation_key,request_hash,expected_revision,committed_revision,decision_count)
		VALUES ($1,$2,$3,$4,$5,$6)`, corpusID, request.OperationKey, requestHash,
		current, revision, len(response.Assignments)); err != nil {
		return nil, fmt.Errorf("record semantic operation: %w", err)
	}
	var decisionBatch pgx.Batch
	for _, assignment := range response.Assignments {
		decision := assignment.GetDecision()
		raw, marshalErr := (proto.MarshalOptions{Deterministic: true}).Marshal(decision)
		if marshalErr != nil {
			return nil, marshalErr
		}
		digest := sha256.Sum256(raw)
		var canonicalID, reviewID *string
		if len(decision.AssignedCanonicalIds) == 1 {
			canonicalID = &decision.AssignedCanonicalIds[0]
			approved := approvalByID[assignment.ProposalId].ReviewID
			reviewID = &approved
		}
		decisionBatch.Queue(`INSERT INTO registry_semantic_decisions
			(corpus_id,operation_key,proposal_id,correlation_id,decision_id,action,
			canonical_id,review_id,registry_revision,decision_payload,decision_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, corpusID, request.OperationKey,
			assignment.ProposalId, assignment.LocalCorrelationId, decision.Meta.RecordId,
			int16(decision.Action), canonicalID, reviewID, revision, raw, hex.EncodeToString(digest[:]))
	}
	results := tx.SendBatch(ctx, &decisionBatch)
	for range response.Assignments {
		if _, err = results.Exec(); err != nil {
			_ = results.Close()
			return nil, fmt.Errorf("record semantic decision: %w", err)
		}
	}
	if err = results.Close(); err != nil {
		return nil, fmt.Errorf("finish semantic decision batch: %w", err)
	}
	if proof != nil {
		if err = verifySemanticLeaseStillLive(ctx, tx, *proof); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit semantic registry operation: %w", err)
	}
	return response, nil
}

func checkReviewedLinks(proposals []*pb.ResolutionProposal, approvals []ReviewedLink) (map[string]ReviewedLink, error) {
	byID := make(map[string]ReviewedLink, len(approvals))
	for _, approval := range approvals {
		if !storageIDPattern.MatchString(approval.ProposalID) ||
			!storageIDPattern.MatchString(approval.CanonicalID) ||
			!storageIDPattern.MatchString(approval.Actor) ||
			!storageIDPattern.MatchString(approval.ReviewID) ||
			approval.Reason == "" || len(approval.Reason) > 1024 || byID[approval.ProposalID].ProposalID != "" {
			return nil, errors.New("invalid or duplicate reviewed LINK authorization")
		}
		byID[approval.ProposalID] = approval
	}
	linkCount := 0
	for _, proposal := range proposals {
		approval, exists := byID[proposal.Meta.RecordId]
		if proposal.Action == pb.ResolutionAction_RESOLUTION_ACTION_LINK {
			linkCount++
			if !exists || approval.CanonicalID != proposal.CandidateIds[0] {
				return nil, errors.New("LINK lacks an exact reviewed candidate authorization")
			}
		} else if exists {
			return nil, errors.New("DEFER cannot carry LINK authorization")
		}
	}
	if len(byID) != linkCount {
		return nil, errors.New("authorization references unknown proposal")
	}
	return byID, nil
}

func semanticRequestHash(request *pb.RegistryResolveRequest, candidates *pb.RegistryCandidateBatch,
	approvals []ReviewedLink) (string, error) {
	stable := proto.Clone(request).(*pb.RegistryResolveRequest)
	stable.Context.RequestId, stable.Context.TraceId, stable.Context.Deadline = "", "", nil
	requestRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(stable)
	if err != nil {
		return "", err
	}
	candidateRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(candidates)
	if err != nil {
		return "", err
	}
	orderedApprovals := append([]ReviewedLink(nil), approvals...)
	sort.Slice(orderedApprovals, func(i, j int) bool {
		return orderedApprovals[i].ProposalID < orderedApprovals[j].ProposalID
	})
	approvalRaw, err := json.Marshal(orderedApprovals)
	if err != nil {
		return "", err
	}
	requestDigest, candidateDigest, approvalDigest := sha256.Sum256(requestRaw),
		sha256.Sum256(candidateRaw), sha256.Sum256(approvalRaw)
	combined := sha256.Sum256(append(append(requestDigest[:], candidateDigest[:]...), approvalDigest[:]...))
	return hex.EncodeToString(combined[:]), nil
}

func (r *Repository) verifySemanticCandidateRead(ctx context.Context, source *pb.ExtractionBatch,
	sourceRef *pb.ArtifactRef, candidates *pb.RegistryCandidateBatch,
	maximumReferences, maximumCandidatesPerMention int) error {
	unique := map[RegistryLookupScope]bool{}
	plans := make([]RegistryCandidatePlan, 0, len(candidates.Lookups))
	for _, lookup := range candidates.Lookups {
		plan := RegistryCandidatePlan{MentionID: lookup.MentionId}
		for _, scope := range lookup.Scopes {
			key := RegistryLookupScope{EntityType: scope.EntityType,
				CanonicalScope: scope.CanonicalScope, NormalizedLookup: scope.NormalizedLookup}
			unique[key] = true
			plan.Scopes = append(plan.Scopes, key)
		}
		plans = append(plans, plan)
	}
	ordered := make([]RegistryLookupScope, 0, len(unique))
	for key := range unique {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].EntityType != ordered[j].EntityType {
			return ordered[i].EntityType < ordered[j].EntityType
		}
		if ordered[i].CanonicalScope != ordered[j].CanonicalScope {
			return ordered[i].CanonicalScope < ordered[j].CanonicalScope
		}
		return ordered[i].NormalizedLookup < ordered[j].NormalizedLookup
	})
	results, revision, err := r.LookupCanonicalAliases(ctx, source.Meta.CorpusId, ordered,
		maximumReferences, maximumReferences)
	if err != nil {
		return err
	}
	if revision != candidates.RegistryRevision {
		return fmt.Errorf("candidate registry revision changed: %w", ErrConflict)
	}
	rebuilt, err := assembleRegistryCandidateResults(source, sourceRef,
		candidates.Dependencies.ProducerManifest, candidates.Meta.RecordId, plans,
		ordered, results, revision, maximumReferences, maximumCandidatesPerMention)
	if err != nil {
		return fmt.Errorf("rebuild authoritative candidate batch: %w", err)
	}
	if !proto.Equal(rebuilt, candidates) {
		return fmt.Errorf("candidate artifact differs from PostgreSQL lookup: %w", ErrConflict)
	}
	return nil
}
