// Menyimpan dan mengoordinasikan lifecycle job durable.
// Peran: menyediakan idempotent submit, SKIP LOCKED claim, lease renewal, fence monotonik,
// checkpoint, dan state transition bagi workflow Go serta worker Rust.
// Kontrak: idempotency key sama hanya menerima payload sama; hasil worker setelah lease
// expired ditolak oleh SaveCheckpoint/TransitionJob; satu claim tidak boleh dimiliki dua worker.
// Benchmark: ukur queue time, claim throughput, p50/p95/p99 query, pool saturation, retry,
// dan contention pada concurrency profil referensi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: primitive job S01, claim/cancellation PARSE/STRUCTURE, retry availability, dan budget
// attempt per-stage aktif; attempt/fence worker tetap monotonik lintas handoff.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

var storageIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (r *Repository) SubmitJob(ctx context.Context, intent JobIntent) (JobRecord, bool, error) {
	if !storageIDPattern.MatchString(intent.JobID) || !storageIDPattern.MatchString(intent.CorpusID) ||
		!storageIDPattern.MatchString(intent.IdempotencyKey) || !sha256Pattern.MatchString(intent.InputFingerprint) ||
		!sha256Pattern.MatchString(intent.RequestHash) || intent.Operation < pb.JobOperation_JOB_OPERATION_INGEST ||
		intent.Operation > pb.JobOperation_JOB_OPERATION_REBUILD || len(intent.RequestPayload) == 0 {
		return JobRecord{}, false, errors.New("invalid job intent")
	}
	payloadHash := sha256.Sum256(intent.RequestPayload)
	if hex.EncodeToString(payloadHash[:]) != intent.RequestHash {
		return JobRecord{}, false, errors.New("request payload hash mismatch")
	}
	request := &pb.IngestionRequest{}
	if err := proto.Unmarshal(intent.RequestPayload, request); err != nil {
		return JobRecord{}, false, fmt.Errorf("decode ingestion request payload: %w", err)
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return JobRecord{}, false, fmt.Errorf("validate ingestion request payload: %w", err)
	}
	if request.CorpusId != intent.CorpusID || request.Operation != intent.Operation || request.IdempotencyKey != intent.IdempotencyKey {
		return JobRecord{}, false, errors.New("job intent differs from persisted request payload")
	}
	fingerprintRequest := proto.Clone(request).(*pb.IngestionRequest)
	fingerprintRequest.IdempotencyKey = ""
	fingerprintPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(fingerprintRequest)
	if err != nil {
		return JobRecord{}, false, fmt.Errorf("encode input fingerprint: %w", err)
	}
	fingerprint := sha256.Sum256(fingerprintPayload)
	if hex.EncodeToString(fingerprint[:]) != intent.InputFingerprint {
		return JobRecord{}, false, errors.New("input fingerprint does not match request semantics")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return JobRecord{}, false, fmt.Errorf("begin submit job: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO corpus_state(corpus_id) VALUES ($1)
        ON CONFLICT (corpus_id) DO NOTHING`, intent.CorpusID); err != nil {
		return JobRecord{}, false, fmt.Errorf("ensure corpus: %w", err)
	}
	row := tx.QueryRow(ctx, `INSERT INTO jobs(
        job_id, corpus_id, operation, state, stage, input_fingerprint,
		idempotency_key, request_hash, request_payload, base_snapshot_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''))
        ON CONFLICT (corpus_id,idempotency_key) DO NOTHING
        RETURNING job_id,corpus_id,operation,state,stage,input_fingerprint,
		  idempotency_key,request_hash,base_snapshot_id,latest_checkpoint_id,attempt,stage_attempt,lease_owner,
          lease_fence,lease_expires_at,cancellation_requested,created_at,updated_at`,
		intent.JobID, intent.CorpusID, int16(intent.Operation), int16(pb.JobState_JOB_STATE_QUEUED),
		int16(intent.InitialStage), intent.InputFingerprint, intent.IdempotencyKey, intent.RequestHash,
		intent.RequestPayload, intent.BaseSnapshotID)
	record, scanErr := scanJob(row)
	reused := false
	if scanErr == pgx.ErrNoRows {
		reused = true
		record, scanErr = scanJob(tx.QueryRow(ctx, `SELECT job_id,corpus_id,operation,state,stage,input_fingerprint,
		  idempotency_key,request_hash,base_snapshot_id,latest_checkpoint_id,attempt,stage_attempt,lease_owner,
          lease_fence,lease_expires_at,cancellation_requested,created_at,updated_at
          FROM jobs WHERE corpus_id=$1 AND idempotency_key=$2`, intent.CorpusID, intent.IdempotencyKey))
		if scanErr == nil && record.RequestHash != intent.RequestHash {
			return JobRecord{}, false, fmt.Errorf("idempotency key reused with a different request: %w", ErrConflict)
		}
	}
	if scanErr != nil {
		return JobRecord{}, false, fmt.Errorf("submit job: %w", scanErr)
	}
	if err = tx.Commit(ctx); err != nil {
		return JobRecord{}, false, fmt.Errorf("commit submit job: %w", err)
	}
	return record, reused, nil
}

func (r *Repository) ClaimJob(ctx context.Context, ownerID string, leaseDuration time.Duration) (JobRecord, error) {
	if !storageIDPattern.MatchString(ownerID) || leaseDuration <= 0 {
		return JobRecord{}, errors.New("valid owner and positive lease duration required")
	}
	row := r.pool.QueryRow(ctx, `WITH candidate AS (
        SELECT job_id FROM jobs
		WHERE cancellation_requested=false AND stage<>$9
		  AND (stage<>$10 OR state IN ($6,$7,$8)) AND (
		  state=$1 OR (state=$2 AND next_attempt_at <= clock_timestamp() AND stage_attempt < max_attempts)
		  OR (state IN ($3,$6,$7,$8) AND lease_expires_at < clock_timestamp()))
        ORDER BY created_at, job_id
        FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE jobs j SET state=CASE WHEN j.state IN ($1,$2) THEN $3 ELSE j.state END,
		attempt=j.attempt+1,stage_attempt=j.stage_attempt+1, lease_owner=$4,
        lease_fence=j.lease_fence+1, lease_expires_at=clock_timestamp()+$5::interval,
        updated_at=clock_timestamp()
      FROM candidate c WHERE j.job_id=c.job_id
      RETURNING j.job_id,j.corpus_id,j.operation,j.state,j.stage,j.input_fingerprint,
		j.idempotency_key,j.request_hash,j.base_snapshot_id,j.latest_checkpoint_id,j.attempt,j.stage_attempt,j.lease_owner,
        j.lease_fence,j.lease_expires_at,j.cancellation_requested,j.created_at,j.updated_at`,
		int16(pb.JobState_JOB_STATE_QUEUED), int16(pb.JobState_JOB_STATE_RETRY_WAIT),
		int16(pb.JobState_JOB_STATE_RUNNING), ownerID, leaseDuration.String(),
		int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobState_JOB_STATE_VALIDATING),
		int16(pb.JobState_JOB_STATE_PUBLISHING), int16(pb.JobStage_JOB_STAGE_PARSE),
		int16(pb.JobStage_JOB_STAGE_STRUCTURE))
	record, err := scanJob(row)
	if err == pgx.ErrNoRows {
		return JobRecord{}, ErrLeaseUnavailable
	}
	if err != nil {
		return JobRecord{}, fmt.Errorf("claim job: %w", err)
	}
	return record, nil
}

// ClaimStructureJob owns the durable PARSE->STRUCTURE handoff. A successful PARSE checkpoint
// advances the globally monotonic worker attempt and fence used by the Rust in-memory registry.
func (r *Repository) ClaimStructureJob(ctx context.Context, ownerID string, leaseDuration time.Duration) (JobRecord, error) {
	if !storageIDPattern.MatchString(ownerID) || leaseDuration <= 0 {
		return JobRecord{}, errors.New("valid owner and positive lease duration required")
	}
	if err := r.finalizeAbandonedCancellations(ctx, pb.JobStage_JOB_STAGE_STRUCTURE); err != nil {
		return JobRecord{}, err
	}
	if _, err := r.pool.Exec(ctx, `WITH exhausted AS (
		SELECT job_id FROM jobs WHERE stage=$2 AND stage_attempt>=max_attempts AND
		(state=$3 OR (state=$4 AND lease_expires_at < clock_timestamp())) AND NOT EXISTS (
		  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$2
		    AND c.terminal_status IS NOT NULL)
		ORDER BY updated_at,job_id FOR UPDATE SKIP LOCKED LIMIT 64)
		UPDATE jobs j SET state=CASE WHEN j.cancellation_requested THEN $5::smallint ELSE $1::smallint END,
		lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
		FROM exhausted e WHERE j.job_id=e.job_id`,
		int16(pb.JobState_JOB_STATE_FAILED), int16(pb.JobStage_JOB_STAGE_STRUCTURE),
		int16(pb.JobState_JOB_STATE_RETRY_WAIT), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(pb.JobState_JOB_STATE_CANCELLED)); err != nil {
		return JobRecord{}, fmt.Errorf("finalize exhausted STRUCTURE jobs: %w", err)
	}
	row := r.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT job_id,stage,state,EXISTS (
		  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$7
		    AND c.terminal_status IS NOT NULL
		) AS recovery_ready FROM jobs
		WHERE cancellation_requested=false AND (
		  (stage=$5 AND state=$6) OR
		  (stage=$7 AND ((state=$1 AND (stage_attempt < max_attempts OR EXISTS (
		      SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$7
		        AND c.terminal_status IS NOT NULL))
		      AND next_attempt_at <= clock_timestamp()) OR
		    (state=$2 AND lease_expires_at < clock_timestamp() AND (stage_attempt < max_attempts OR EXISTS (
		      SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$7
		        AND c.terminal_status IS NOT NULL))))))
		ORDER BY created_at,job_id FOR UPDATE SKIP LOCKED LIMIT 1)
		UPDATE jobs j SET state=$2,stage=$7,
		attempt=j.attempt+1,stage_attempt=CASE WHEN c.stage=$5 AND c.state=$6 THEN 1
		  WHEN c.recovery_ready THEN j.stage_attempt ELSE j.stage_attempt+1 END,
		lease_owner=$3,lease_fence=j.lease_fence+1,
		lease_expires_at=clock_timestamp()+$4::interval,updated_at=clock_timestamp()
		FROM candidate c WHERE j.job_id=c.job_id
		RETURNING j.job_id,j.corpus_id,j.operation,j.state,j.stage,j.input_fingerprint,
		j.idempotency_key,j.request_hash,j.base_snapshot_id,j.latest_checkpoint_id,j.attempt,j.stage_attempt,j.lease_owner,
		j.lease_fence,j.lease_expires_at,j.cancellation_requested,j.created_at,j.updated_at`,
		int16(pb.JobState_JOB_STATE_RETRY_WAIT), int16(pb.JobState_JOB_STATE_RUNNING),
		ownerID, leaseDuration.String(), int16(pb.JobStage_JOB_STAGE_PARSE),
		int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobStage_JOB_STAGE_STRUCTURE))
	record, err := scanJob(row)
	if err == pgx.ErrNoRows {
		return JobRecord{}, ErrLeaseUnavailable
	}
	if err != nil {
		return JobRecord{}, fmt.Errorf("claim STRUCTURE job: %w", err)
	}
	return record, nil
}

// ClaimParseJob leases only new/retrying PARSE inputs or an expired PARSE attempt. Later pipeline
// states remain available to their owning coordinators and cannot be consumed by the PARSE daemon.
func (r *Repository) ClaimParseJob(ctx context.Context, ownerID string, leaseDuration time.Duration) (JobRecord, error) {
	if !storageIDPattern.MatchString(ownerID) || leaseDuration <= 0 {
		return JobRecord{}, errors.New("valid owner and positive lease duration required")
	}
	if err := r.finalizeAbandonedCancellations(ctx, pb.JobStage_JOB_STAGE_PARSE); err != nil {
		return JobRecord{}, err
	}
	if _, err := r.pool.Exec(ctx, `WITH exhausted AS (
		SELECT job_id FROM jobs WHERE stage=$2 AND stage_attempt>=max_attempts AND
		(state=$3 OR (state=$4 AND lease_expires_at < clock_timestamp())) AND NOT EXISTS (
		  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$2
		    AND c.terminal_status IS NOT NULL)
		ORDER BY updated_at,job_id FOR UPDATE SKIP LOCKED LIMIT 64)
		UPDATE jobs j SET state=CASE WHEN j.cancellation_requested THEN $5::smallint ELSE $1::smallint END,
		lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
		FROM exhausted e WHERE j.job_id=e.job_id`,
		int16(pb.JobState_JOB_STATE_FAILED), int16(pb.JobStage_JOB_STAGE_PARSE),
		int16(pb.JobState_JOB_STATE_RETRY_WAIT), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(pb.JobState_JOB_STATE_CANCELLED)); err != nil {
		return JobRecord{}, fmt.Errorf("finalize exhausted PARSE jobs: %w", err)
	}
	row := r.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT job_id,EXISTS (
		  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id AND c.stage=$6
		    AND c.terminal_status IS NOT NULL
		) AS recovery_ready FROM jobs
        WHERE cancellation_requested=false
          AND stage=$6
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
		int16(pb.JobStage_JOB_STAGE_PARSE))
	record, err := scanJob(row)
	if err == pgx.ErrNoRows {
		return JobRecord{}, ErrLeaseUnavailable
	}
	if err != nil {
		return JobRecord{}, fmt.Errorf("claim PARSE job: %w", err)
	}
	return record, nil
}

// finalizeAbandonedCancellations prevents a cancellation from stranding work after its owner dies.
// A live RUNNING lease keeps responsibility until completion; unleased or expired work terminates.
func (r *Repository) finalizeAbandonedCancellations(ctx context.Context, stage pb.JobStage) error {
	_, err := r.pool.Exec(ctx, `WITH abandoned AS (
		SELECT job_id FROM jobs WHERE stage=$1 AND cancellation_requested=true AND (
		  state IN ($2,$3,$4) OR (state=$5 AND lease_expires_at < clock_timestamp()))
		ORDER BY updated_at,job_id FOR UPDATE SKIP LOCKED LIMIT 64)
		UPDATE jobs j SET state=$6,lease_owner=NULL,lease_expires_at=NULL,
		next_attempt_at='-infinity'::timestamptz,updated_at=clock_timestamp()
		FROM abandoned a WHERE j.job_id=a.job_id`, int16(stage),
		int16(pb.JobState_JOB_STATE_QUEUED), int16(pb.JobState_JOB_STATE_RETRY_WAIT),
		int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(pb.JobState_JOB_STATE_CANCELLED))
	if err != nil {
		return fmt.Errorf("finalize abandoned %s cancellation: %w", stage, err)
	}
	return nil
}

func (r *Repository) RenewLease(ctx context.Context, jobID, ownerID string, fence uint64, leaseDuration time.Duration) (time.Time, error) {
	if leaseDuration <= 0 || fence == 0 {
		return time.Time{}, errors.New("positive fence and lease duration required")
	}
	var expires time.Time
	err := r.pool.QueryRow(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+$4::interval,
      updated_at=clock_timestamp() WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3
		AND state IN ($5,$6,$7,$8) AND lease_expires_at >= clock_timestamp()
		AND cancellation_requested=false
      RETURNING lease_expires_at`, jobID, ownerID, int64(fence), leaseDuration.String(),
		int16(pb.JobState_JOB_STATE_RUNNING), int16(pb.JobState_JOB_STATE_STAGED),
		int16(pb.JobState_JOB_STATE_VALIDATING), int16(pb.JobState_JOB_STATE_PUBLISHING)).Scan(&expires)
	if err == pgx.ErrNoRows {
		return time.Time{}, ErrStaleFence
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("renew lease: %w", err)
	}
	return expires, nil
}

func (r *Repository) CancellationRequested(ctx context.Context, jobID, ownerID string, fence uint64) (bool, error) {
	if !storageIDPattern.MatchString(jobID) || !storageIDPattern.MatchString(ownerID) || fence == 0 {
		return false, errors.New("valid job, owner, and fence required")
	}
	var requested bool
	err := r.pool.QueryRow(ctx, `SELECT cancellation_requested FROM jobs
		WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3
		AND state IN ($4,$5,$6,$7) AND lease_expires_at >= clock_timestamp()`,
		jobID, ownerID, int64(fence), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobState_JOB_STATE_VALIDATING),
		int16(pb.JobState_JOB_STATE_PUBLISHING)).Scan(&requested)
	if err == pgx.ErrNoRows {
		return false, ErrStaleFence
	}
	if err != nil {
		return false, fmt.Errorf("read job cancellation: %w", err)
	}
	return requested, nil
}

// CompleteWorkerAttempt atomically gives durable cancellation precedence over a worker result.
// It prevents a cancellation arriving after the RPC monitor exits from stranding a RUNNING job.
func (r *Repository) CompleteWorkerAttempt(ctx context.Context, jobID, ownerID string, fence uint64, desired pb.JobState, retryDelay time.Duration) (pb.JobState, error) {
	if !storageIDPattern.MatchString(jobID) || !storageIDPattern.MatchString(ownerID) || fence == 0 ||
		!domain.AllowedJobTransition(pb.JobState_JOB_STATE_RUNNING, desired) || retryDelay < 0 ||
		desired == pb.JobState_JOB_STATE_RETRY_WAIT && retryDelay == 0 ||
		desired != pb.JobState_JOB_STATE_RETRY_WAIT && retryDelay != 0 {
		return pb.JobState_JOB_STATE_UNSPECIFIED, errors.New("valid worker completion identity and state required")
	}
	retryMicroseconds := retryDelay.Microseconds()
	if retryDelay > 0 && retryMicroseconds == 0 {
		retryMicroseconds = 1
	}
	var actual int16
	err := r.pool.QueryRow(ctx, `UPDATE jobs SET
		state=CASE WHEN cancellation_requested THEN $6::smallint
			WHEN $5::smallint=$7::smallint AND stage_attempt>=max_attempts AND NOT EXISTS (
			  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id
			    AND c.stage=jobs.stage AND c.terminal_status IS NOT NULL)
			THEN $8::smallint ELSE $5::smallint END,
		updated_at=clock_timestamp(),
		next_attempt_at=CASE WHEN NOT cancellation_requested AND $5::smallint=$7::smallint
			AND (stage_attempt<max_attempts OR EXISTS (
			  SELECT 1 FROM job_checkpoints c WHERE c.checkpoint_id=jobs.latest_checkpoint_id
			    AND c.stage=jobs.stage AND c.terminal_status IS NOT NULL))
			THEN clock_timestamp()+$9::bigint*interval '1 microsecond' ELSE '-infinity'::timestamptz END,
		lease_owner=CASE WHEN cancellation_requested OR $5::smallint IN (3,7,8,9,10) THEN NULL ELSE lease_owner END,
		lease_expires_at=CASE WHEN cancellation_requested OR $5::smallint IN (3,7,8,9,10) THEN NULL ELSE lease_expires_at END
		WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3 AND state=$4
		AND lease_expires_at >= clock_timestamp()
		RETURNING state`, jobID, ownerID, int64(fence), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(desired), int16(pb.JobState_JOB_STATE_CANCELLED), int16(pb.JobState_JOB_STATE_RETRY_WAIT),
		int16(pb.JobState_JOB_STATE_FAILED), retryMicroseconds).Scan(&actual)
	if err == pgx.ErrNoRows {
		return pb.JobState_JOB_STATE_UNSPECIFIED, ErrStaleFence
	}
	if err != nil {
		return pb.JobState_JOB_STATE_UNSPECIFIED, fmt.Errorf("complete worker attempt: %w", err)
	}
	return pb.JobState(actual), nil
}

func (r *Repository) SaveCheckpoint(ctx context.Context, checkpoint *pb.Checkpoint, ownerID string) error {
	if checkpoint == nil || checkpoint.GetMeta() == nil || checkpoint.GetJobId() == "" || checkpoint.GetFence() == 0 {
		return errors.New("complete checkpoint required")
	}
	switch checkpoint.TerminalStatus {
	case pb.CompletionStatus_COMPLETION_STATUS_UNSPECIFIED,
		pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		pb.CompletionStatus_COMPLETION_STATUS_FAILED,
		pb.CompletionStatus_COMPLETION_STATUS_CANCELLED:
	default:
		return errors.New("valid checkpoint terminal status required")
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	digestBytes := sha256.Sum256(payload)
	digest := hex.EncodeToString(digestBytes[:])
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin checkpoint: %w", err)
	}
	defer tx.Rollback(ctx)
	var currentFence int64
	var currentStage int16
	err = tx.QueryRow(ctx, `SELECT lease_fence,stage FROM jobs WHERE job_id=$1 AND lease_owner=$2
		AND lease_fence=$3 AND state=$4 AND lease_expires_at >= clock_timestamp()
		AND corpus_id=$5 FOR UPDATE`, checkpoint.JobId, ownerID, int64(checkpoint.Fence),
		int16(pb.JobState_JOB_STATE_RUNNING), checkpoint.Meta.CorpusId).Scan(&currentFence, &currentStage)
	if err == pgx.ErrNoRows {
		return ErrStaleFence
	}
	if err != nil {
		return fmt.Errorf("verify checkpoint fence: %w", err)
	}
	if int16(checkpoint.Stage) < currentStage {
		return fmt.Errorf("checkpoint stage regressed: %w", ErrConflict)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status)
      VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7::smallint,0)) ON CONFLICT (checkpoint_id) DO NOTHING`, checkpoint.Meta.RecordId,
		checkpoint.JobId, int16(checkpoint.Stage), int64(checkpoint.Fence), payload, digest, int16(checkpoint.TerminalStatus))
	if err != nil {
		return fmt.Errorf("insert checkpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var existing string
		if err = tx.QueryRow(ctx, "SELECT payload_hash FROM job_checkpoints WHERE checkpoint_id=$1", checkpoint.Meta.RecordId).Scan(&existing); err != nil {
			return fmt.Errorf("read checkpoint conflict: %w", err)
		}
		if existing != digest {
			return fmt.Errorf("checkpoint id reused with different bytes: %w", ErrConflict)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs SET stage=$2,latest_checkpoint_id=$4,updated_at=clock_timestamp()
		WHERE job_id=$1 AND lease_fence=$3`, checkpoint.JobId, int16(checkpoint.Stage),
		int64(checkpoint.Fence), checkpoint.Meta.RecordId); err != nil {
		return fmt.Errorf("advance checkpoint stage: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *Repository) TransitionJob(ctx context.Context, jobID, ownerID string, fence uint64, expected, next pb.JobState) error {
	if !domain.AllowedJobTransition(expected, next) {
		return fmt.Errorf("job transition is not workflow-owned or allowed: %w", ErrConflict)
	}
	tag, err := r.pool.Exec(ctx, `UPDATE jobs SET state=$5::smallint,updated_at=clock_timestamp(),
		lease_owner=CASE WHEN $5::smallint IN (3,7,8,9,10) THEN NULL ELSE lease_owner END,
		lease_expires_at=CASE WHEN $5::smallint IN (3,7,8,9,10) THEN NULL ELSE lease_expires_at END
		WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3 AND state=$4
		AND lease_expires_at >= clock_timestamp()
		AND (cancellation_requested=false OR $5::smallint=$6::smallint)`,
		jobID, ownerID, int64(fence), int16(expected), int16(next),
		int16(pb.JobState_JOB_STATE_CANCELLED))
	if err != nil {
		return fmt.Errorf("transition job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleFence
	}
	return nil
}

func scanJob(row pgx.Row) (JobRecord, error) {
	var rec JobRecord
	var operation, state, stage int16
	var base, checkpoint, owner sql.NullString
	var expires sql.NullTime
	var attempt, stageAttempt int32
	var fence int64
	err := row.Scan(&rec.JobID, &rec.CorpusID, &operation, &state, &stage, &rec.InputFingerprint,
		&rec.IdempotencyKey, &rec.RequestHash, &base, &checkpoint, &attempt, &stageAttempt, &owner, &fence, &expires,
		&rec.CancellationRequested, &rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		return JobRecord{}, err
	}
	rec.Operation = pb.JobOperation(operation)
	rec.State = pb.JobState(state)
	rec.Stage = pb.JobStage(stage)
	rec.BaseSnapshotID = base.String
	rec.LatestCheckpointID = checkpoint.String
	rec.Attempt = uint32(attempt)
	rec.StageAttempt = uint32(stageAttempt)
	rec.LeaseOwner = owner.String
	rec.LeaseFence = uint64(fence)
	if expires.Valid {
		rec.LeaseExpiresAt = expires.Time
	}
	return rec, nil
}

func (r *Repository) LoadIngestionRequest(ctx context.Context, jobID string) (*pb.IngestionRequest, error) {
	var payload []byte
	var expectedHash string
	err := r.pool.QueryRow(ctx, `SELECT request_payload,request_hash FROM jobs WHERE job_id=$1`, jobID).Scan(&payload, &expectedHash)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("claimed job request is missing: %w", domain.ErrPersistentIntegrity)
	}
	if err != nil {
		return nil, fmt.Errorf("load ingestion request: %w", err)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != expectedHash {
		return nil, fmt.Errorf("persisted ingestion request checksum mismatch: %w", errors.Join(ErrConflict, domain.ErrPersistentIntegrity))
	}
	request := &pb.IngestionRequest{}
	if err = proto.Unmarshal(payload, request); err != nil {
		return nil, fmt.Errorf("decode persisted ingestion request: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	if err = domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("validate persisted ingestion request: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	return request, nil
}

func (r *Repository) LoadLatestCheckpoint(ctx context.Context, jobID string) (*pb.Checkpoint, error) {
	var payload []byte
	var expectedHash string
	var storedTerminal sql.NullInt16
	err := r.pool.QueryRow(ctx, `SELECT c.payload,c.payload_hash,c.terminal_status FROM jobs j
		JOIN job_checkpoints c ON c.checkpoint_id=j.latest_checkpoint_id WHERE j.job_id=$1`, jobID).Scan(&payload, &expectedHash, &storedTerminal)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load latest checkpoint: %w", err)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != expectedHash {
		return nil, fmt.Errorf("persisted checkpoint checksum mismatch: %w", errors.Join(ErrConflict, domain.ErrPersistentIntegrity))
	}
	checkpoint := &pb.Checkpoint{}
	if err = proto.Unmarshal(payload, checkpoint); err != nil {
		return nil, fmt.Errorf("decode persisted checkpoint: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	if err = domain.ValidateWire(checkpoint, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("validate persisted checkpoint: %w", errors.Join(err, domain.ErrPersistentIntegrity))
	}
	if storedTerminal.Valid != (checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_UNSPECIFIED) ||
		storedTerminal.Valid && int16(checkpoint.TerminalStatus) != storedTerminal.Int16 {
		return nil, fmt.Errorf("persisted checkpoint terminal status mismatch: %w", domain.ErrPersistentIntegrity)
	}
	return checkpoint, nil
}
