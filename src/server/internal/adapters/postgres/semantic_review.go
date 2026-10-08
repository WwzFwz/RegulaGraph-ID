// Persists an operator-reviewed LINK/DEFER batch, immutable replay intent and resume in
// one PostgreSQL transaction. The workflow authenticates immutable model bytes and the
// operator; this adapter authenticates queue/checkpoint/revision/state under locks. Lock
// order is corpus then job, matching registry CAS. Exact replay never revives a job or
// resets retry budgets. Measure lock/commit p95/p99 and conflict rates; required targets
// in configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) LoadSemanticReviewQueue(ctx context.Context, corpus, jobID string) (domain.SemanticReviewQueue, error) {
	q := domain.SemanticReviewQueue{CorpusID: corpus, JobID: jobID}
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(jobID) {
		return q, errors.New("valid corpus/job required")
	}
	ids := make([]string, 4)
	err := r.pool.QueryRow(ctx, `SELECT source_checkpoint_id,source_artifact_id,candidate_artifact_id,input_artifact_id,output_artifact_id FROM semantic_proposal_queue WHERE corpus_id=$1 AND job_id=$2`, corpus, jobID).Scan(&q.SourceCheckpointID, &ids[0], &ids[1], &ids[2], &ids[3])
	if errors.Is(err, pgx.ErrNoRows) {
		return q, ErrNotFound
	}
	if err != nil {
		return q, err
	}
	for i, target := range []**pb.ArtifactRef{&q.Source, &q.Candidates, &q.Input, &q.Output} {
		*target, err = r.LoadArtifact(ctx, corpus, ids[i])
		if err != nil {
			return q, err
		}
	}
	return q, nil
}

// AcceptSemanticReview trusts only the internal verified workflow, not raw API payloads.
// First acceptance resets the stage-local retry budget for execution after human review;
// global attempt and fence counters stay monotonic. It does not commit canonical changes.
func (r *Repository) AcceptSemanticReview(ctx context.Context, review domain.SemanticReviewCommit) (bool, error) {
	q, intent := review.Queue, review.Intent
	if ctx == nil || !storageIDPattern.MatchString(q.CorpusID) || !storageIDPattern.MatchString(q.JobID) || !storageIDPattern.MatchString(review.Actor) ||
		strings.TrimSpace(review.Reason) == "" || len(review.Reason) > 1024 || !utf8.ValidString(review.Reason) || strings.ContainsRune(review.Reason, 0) ||
		intent.Request == nil || intent.Preview == nil || intent.Request.GetContext().GetCorpusId() != q.CorpusID || intent.SourceCheckpointID != q.SourceCheckpointID ||
		!proto.Equal(intent.CandidateRef, q.Candidates) || !proto.Equal(intent.Preview.SourceExtractionBatch, q.Source) {
		return false, errors.New("complete reviewed intent required")
	}
	stored, err := r.LoadSemanticReviewQueue(ctx, q.CorpusID, q.JobID)
	if err != nil {
		return false, err
	}
	if stored.SourceCheckpointID != q.SourceCheckpointID {
		return false, ErrConflict
	}
	for i, ref := range []*pb.ArtifactRef{q.Source, q.Candidates, q.Input, q.Output} {
		if !proto.Equal(ref, []*pb.ArtifactRef{stored.Source, stored.Candidates, stored.Input, stored.Output}[i]) {
			return false, ErrConflict
		}
	}
	for _, value := range []proto.Message{intent.Request, intent.Preview} {
		if err = domain.ValidateWire(value, domain.DefaultWireLimits); err != nil {
			return false, err
		}
	}
	if _, err = domain.PreviewSemanticResolutionReceipt(intent.Request, intent.Approvals); err != nil {
		return false, err
	}
	approvals, err := checkReviewedLinks(intent.Request.Proposals, intent.Approvals)
	if err != nil {
		return false, err
	}
	for _, approval := range approvals {
		if approval.Actor != review.Actor || approval.Reason != review.Reason {
			return false, ErrConflict
		}
	}
	payload, err := encodeSemanticIntent(intent)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var revision uint64
	if err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, q.CorpusID).Scan(&revision); err != nil {
		return false, err
	}
	var state, stage int16
	var checkpointID string
	var cancelled bool
	var fence uint64
	if err = tx.QueryRow(ctx, `SELECT state,stage,latest_checkpoint_id,cancellation_requested,lease_fence FROM jobs WHERE corpus_id=$1 AND job_id=$2 FOR UPDATE`, q.CorpusID, q.JobID).Scan(&state, &stage, &checkpointID, &cancelled, &fence); err != nil {
		return false, err
	}
	var actor, reason, outputHash, intentHash string
	var expected uint64
	err = tx.QueryRow(ctx, `SELECT actor,reason,output_hash,expected_revision,intent_hash FROM semantic_batch_reviews WHERE corpus_id=$1 AND job_id=$2`, q.CorpusID, q.JobID).Scan(&actor, &reason, &outputHash, &expected, &intentHash)
	if err == nil {
		if actor != review.Actor || reason != review.Reason || outputHash != q.Output.ContentHash.Sha256 || expected != intent.Request.ExpectedRevision || intentHash != hash {
			return false, ErrConflict
		}
		return false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if cancelled || state != int16(pb.JobState_JOB_STATE_WAITING_REVIEW) || stage != int16(pb.JobStage_JOB_STAGE_RESOLVE) || checkpointID != q.SourceCheckpointID {
		return false, ErrConflict
	}
	if revision != intent.Request.ExpectedRevision {
		return false, domain.ErrResolutionReplan
	}
	var cpRaw []byte
	var cpHash string
	if err = tx.QueryRow(ctx, `SELECT payload,payload_hash FROM job_checkpoints WHERE checkpoint_id=$1 AND job_id=$2 AND stage=$3 AND terminal_status=$4 FOR SHARE`, checkpointID, q.JobID, int16(pb.JobStage_JOB_STAGE_EXTRACT), int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED)).Scan(&cpRaw, &cpHash); err != nil {
		return false, err
	}
	cpDigest := sha256.Sum256(cpRaw)
	cp := new(pb.Checkpoint)
	if hex.EncodeToString(cpDigest[:]) != cpHash || domain.DecodeWire(cpRaw, cp, domain.DefaultWireLimits) != nil || cp.GetMeta().GetCorpusId() != q.CorpusID || cp.GetMeta().GetRecordId() != checkpointID || cp.JobId != q.JobID || cp.Stage != pb.JobStage_JOB_STAGE_EXTRACT || cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || cp.Fence == 0 || cp.Fence >= fence || len(cp.CompletedBatchKeys) != 1 || len(cp.ArtifactHashes) != 1 || cp.CompletedBatchKeys[0] != q.Source.ArtifactId || !proto.Equal(cp.ArtifactHashes[0], q.Source.ContentHash) {
		return false, domain.ErrPersistentIntegrity
	}
	var inserts pgx.Batch
	for _, proposal := range intent.Request.Proposals {
		approval, ok := approvals[proposal.Meta.RecordId]
		if !ok {
			continue
		}
		raw, encodeErr := (proto.MarshalOptions{Deterministic: true}).Marshal(proposal)
		if encodeErr != nil {
			return false, encodeErr
		}
		sum := sha256.Sum256(raw)
		inserts.Queue(`INSERT INTO registry_semantic_reviews(corpus_id,review_id,source_artifact_id,candidate_artifact_id,candidate_hash,proposal_id,proposal_hash,canonical_id,actor,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, q.CorpusID, approval.ReviewID, q.Source.ArtifactId, q.Candidates.ArtifactId, q.Candidates.ContentHash.Sha256, approval.ProposalID, hex.EncodeToString(sum[:]), approval.CanonicalID, review.Actor, review.Reason)
	}
	if inserts.Len() > 0 {
		results := tx.SendBatch(ctx, &inserts)
		if err = results.Close(); err != nil {
			return false, fmt.Errorf("persist LINK reviews: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO semantic_resolution_intents(job_id,corpus_id,source_checkpoint_id,operation_key,payload,payload_hash) VALUES($1,$2,$3,$4,$5,$6)`, q.JobID, q.CorpusID, q.SourceCheckpointID, intent.Request.OperationKey, payload, hash); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO semantic_batch_reviews(job_id,corpus_id,actor,reason,output_hash,expected_revision,intent_hash) VALUES($1,$2,$3,$4,$5,$6,$7)`, q.JobID, q.CorpusID, review.Actor, review.Reason, q.Output.ContentHash.Sha256, revision, hash); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs SET state=$2,stage_attempt=0,lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=clock_timestamp(),updated_at=clock_timestamp() WHERE job_id=$1`, q.JobID, int16(pb.JobState_JOB_STATE_RETRY_WAIT)); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
