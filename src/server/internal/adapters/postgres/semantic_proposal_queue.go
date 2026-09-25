// Parks verified advisory model artifacts and their EXTRACT checkpoint in one fenced transaction.
// The queue is a durable locator for review, never evidence of an approved LINK. Workflow owns
// byte/model validation; this adapter authenticates registered metadata, corpus and live lease,
// and gives durable cancellation precedence over queue admission. Measure lock/write p95/p99,
// queue age and retries against configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) ParkSemanticProposal(ctx context.Context, proof domain.SemanticJobFence,
	sourceRef *pb.ArtifactRef, source *pb.ExtractionBatch, candidate, input, output *pb.ArtifactRef) (pb.JobState, error) {
	if ctx == nil || source == nil || source.GetMeta() == nil || sourceRef == nil ||
		!storageIDPattern.MatchString(proof.JobID) || !storageIDPattern.MatchString(proof.OwnerID) || proof.Fence == 0 {
		return pb.JobState_JOB_STATE_UNSPECIFIED, errors.New("fenced proposal and source required")
	}
	corpus := source.Meta.CorpusId
	seen := map[string]bool{}
	for _, ref := range []*pb.ArtifactRef{sourceRef, candidate, input, output} {
		if ref == nil || domain.ValidateWire(ref, domain.DefaultWireLimits) != nil || ref.ByteSize == 0 || seen[ref.ArtifactId] {
			return pb.JobState_JOB_STATE_UNSPECIFIED, errors.New("distinct bounded proposal artifacts required")
		}
		seen[ref.ArtifactId] = true
		stored, err := r.LoadArtifact(ctx, corpus, ref.ArtifactId)
		if err != nil {
			return pb.JobState_JOB_STATE_UNSPECIFIED, err
		}
		if !proto.Equal(stored, ref) {
			return pb.JobState_JOB_STATE_UNSPECIFIED, domain.ErrPersistentIntegrity
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return pb.JobState_JOB_STATE_UNSPECIFIED, err
	}
	defer tx.Rollback(ctx)
	// Acquire the exclusive job lock before verifySemanticJobFence's shared locks. This
	// serializes cancellation and avoids lock-upgrade deadlocks with competing completions.
	var cancelled bool
	err = tx.QueryRow(ctx, `SELECT cancellation_requested FROM jobs WHERE job_id=$1 AND corpus_id=$2
  AND lease_owner=$3 AND lease_fence=$4 AND state=$5 AND stage=$6 AND lease_expires_at>clock_timestamp()
  FOR UPDATE`, proof.JobID, corpus, proof.OwnerID, int64(proof.Fence), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(pb.JobStage_JOB_STAGE_RESOLVE)).Scan(&cancelled)
	if err == pgx.ErrNoRows {
		return pb.JobState_JOB_STATE_UNSPECIFIED, ErrStaleFence
	}
	if err != nil {
		return pb.JobState_JOB_STATE_UNSPECIFIED, err
	}
	desired := pb.JobState_JOB_STATE_WAITING_REVIEW
	if cancelled {
		desired = pb.JobState_JOB_STATE_CANCELLED
	} else {
		if err = verifySemanticJobFence(ctx, tx, proof, corpus, sourceRef, source); err != nil {
			return pb.JobState_JOB_STATE_UNSPECIFIED, err
		}
		tag, insertErr := tx.Exec(ctx, `INSERT INTO semantic_proposal_queue
   (job_id,corpus_id,source_checkpoint_id,source_artifact_id,candidate_artifact_id,input_artifact_id,output_artifact_id)
   VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (job_id) DO NOTHING`, proof.JobID, corpus, proof.SourceCheckpointID,
			sourceRef.ArtifactId, candidate.ArtifactId, input.ArtifactId, output.ArtifactId)
		if insertErr != nil {
			return pb.JobState_JOB_STATE_UNSPECIFIED, insertErr
		}
		if tag.RowsAffected() != 1 {
			return pb.JobState_JOB_STATE_UNSPECIFIED, fmt.Errorf("proposal queue already exists: %w", ErrConflict)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE jobs SET state=$5,lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
  WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3 AND state=$4 AND lease_expires_at>clock_timestamp()`,
		proof.JobID, proof.OwnerID, int64(proof.Fence), int16(pb.JobState_JOB_STATE_RUNNING), int16(desired))
	if err != nil {
		return pb.JobState_JOB_STATE_UNSPECIFIED, err
	}
	if tag.RowsAffected() != 1 {
		return pb.JobState_JOB_STATE_UNSPECIFIED, ErrStaleFence
	}
	if err = tx.Commit(ctx); err != nil {
		return pb.JobState_JOB_STATE_UNSPECIFIED, err
	}
	return desired, nil
}

// LoadPendingSemanticProposal locates an immutable advisory response within its corpus.
// A review consumer must read verified bytes and revalidate all dependencies before approval.
func (r *Repository) LoadPendingSemanticProposal(ctx context.Context, corpus, jobID string) (*pb.ArtifactRef, error) {
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(jobID) {
		return nil, errors.New("valid corpus/job required")
	}
	var output string
	err := r.pool.QueryRow(ctx, `SELECT output_artifact_id FROM semantic_proposal_queue WHERE corpus_id=$1 AND job_id=$2`, corpus, jobID).Scan(&output)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.LoadArtifact(ctx, corpus, output)
}
