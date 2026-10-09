// Claims only inventory-owned ASSEMBLE work using bounded retries and worker fences.
// A publication must still be live and its source uncancelled at claim time.
// Claim is scheduling permission, not publication authority: dispatch and commit
// must recheck source bytes, cancellation and publisher fence. Completed STAGED
// children remain owned by the inventory coordinator, never the generic claimer.
// Measure queue/claim latency and recovery against configs/benchmark-targets.yaml.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"time"
)

func (r *Repository) ClaimGraphJob(ctx context.Context, ownerID string, leaseDuration time.Duration) (JobRecord, error) {
	if !storageIDPattern.MatchString(ownerID) || leaseDuration <= 0 {
		return JobRecord{}, errors.New("valid owner and positive lease duration required")
	}
	if err := r.finalizeAbandonedCancellations(ctx, pb.JobStage_JOB_STAGE_ASSEMBLE); err != nil {
		return JobRecord{}, err
	}
	if _, err := r.pool.Exec(ctx, `WITH exhausted AS (
		SELECT job_id FROM jobs WHERE stage=$2 AND stage_attempt>=max_attempts AND
		EXISTS (SELECT 1 FROM graph_job_assignments a WHERE a.job_id=jobs.job_id) AND
		(state=$3 OR (state=$4 AND lease_expires_at < clock_timestamp())) AND NOT EXISTS (
		  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$2
		    AND c.terminal_status IS NOT NULL)
		ORDER BY updated_at,job_id FOR UPDATE SKIP LOCKED LIMIT 64)
		UPDATE jobs j SET state=CASE WHEN j.cancellation_requested THEN $5::smallint ELSE $1::smallint END,
		lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
		FROM exhausted e WHERE j.job_id=e.job_id`,
		int16(pb.JobState_JOB_STATE_FAILED), int16(pb.JobStage_JOB_STAGE_ASSEMBLE),
		int16(pb.JobState_JOB_STATE_RETRY_WAIT), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(pb.JobState_JOB_STATE_CANCELLED)); err != nil {
		return JobRecord{}, fmt.Errorf("finalize exhausted ASSEMBLE jobs: %w", err)
	}
	row := r.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT job_id,EXISTS (
		  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$6
		    AND c.terminal_status IS NOT NULL
		) AS recovery_ready FROM jobs
        WHERE cancellation_requested=false
          AND stage=$6
          AND EXISTS (
            SELECT 1 FROM graph_job_assignments a
            JOIN graph_job_inventories i ON i.publication_id=a.publication_id
            JOIN snapshots s ON s.publication_id=i.publication_id
            JOIN corpus_state c ON c.corpus_id=i.corpus_id
            JOIN jobs source ON source.job_id=a.source_job_id
            WHERE a.job_id=jobs.job_id AND i.corpus_id=jobs.corpus_id
              AND s.fence=i.fence AND c.publisher_fence=i.fence
              AND s.state IN ($7,$8) AND NOT source.cancellation_requested
              AND c.active_snapshot_id=s.parent_snapshot_id
              AND EXISTS (SELECT 1 FROM graph_source_bindings b
                WHERE b.publication_id=i.publication_id AND b.source_job_id=source.job_id
                AND b.source_checkpoint_id=source.latest_checkpoint_id)
              AND EXISTS (SELECT 1 FROM snapshot_registry_bindings v
                WHERE v.publication_id=s.publication_id AND v.fence=s.fence
                AND v.registry_revision BETWEEN c.registry_history_floor AND c.registry_revision))
		  AND (state=$1 OR
		    (state=$2 AND next_attempt_at <= clock_timestamp() AND (stage_attempt < max_attempts OR EXISTS (
		      SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$6
		        AND c.terminal_status IS NOT NULL))) OR
		    (state=$3 AND lease_expires_at < clock_timestamp() AND (stage_attempt < max_attempts OR EXISTS (
		      SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$6
		        AND c.terminal_status IS NOT NULL))))
        ORDER BY created_at, job_id
        FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE jobs j SET state=$3,attempt=j.attempt+1,
		stage_attempt=CASE WHEN c.recovery_ready THEN j.stage_attempt ELSE j.stage_attempt+1 END,lease_owner=$4,
        lease_fence=j.lease_fence+1,lease_expires_at=clock_timestamp()+$5::interval,
        updated_at=clock_timestamp()
      FROM candidate c WHERE j.job_id=c.job_id
      RETURNING j.job_id,j.corpus_id,j.operation,j.state,j.stage,j.input_fingerprint,
		j.idempotency_key,j.request_hash,j.base_snapshot_id,j.latest_checkpoint_id,j.attempt,j.stage_attempt,j.lease_owner,
        j.lease_fence,j.lease_expires_at,j.cancellation_requested,j.created_at,j.updated_at`,
		int16(pb.JobState_JOB_STATE_QUEUED), int16(pb.JobState_JOB_STATE_RETRY_WAIT),
		int16(pb.JobState_JOB_STATE_RUNNING), ownerID, leaseDuration.String(),
		int16(pb.JobStage_JOB_STAGE_ASSEMBLE), int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING),
		int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING))
	record, err := scanJob(row)
	if err == pgx.ErrNoRows {
		return JobRecord{}, ErrLeaseUnavailable
	}
	if err != nil {
		return JobRecord{}, fmt.Errorf("claim ASSEMBLE job: %w", err)
	}
	return record, nil
}
