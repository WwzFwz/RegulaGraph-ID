// Authenticates the claimed RESOLVE lease and successful EXTRACT checkpoint inside the
// semantic registry transaction. Locking the job row prevents a concurrent owner/fence or
// cancellation transition while decisions are committed; a final database-clock lease check
// rejects writes after expiry. Measure lock wait and conflict/retry rates at concurrency;
// required targets in configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func verifySemanticJobFence(ctx context.Context, tx pgx.Tx, proof SemanticJobFence,
	corpusID string, sourceRef *pb.ArtifactRef, source *pb.ExtractionBatch) error {
	var storedCorpus, owner, checkpointID, payloadHash string
	var state, stage, checkpointStage int16
	var fence, checkpointFence int64
	var leaseLive, cancelled bool
	var terminal sql.NullInt16
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT j.corpus_id,j.state,j.stage,j.lease_owner,j.lease_fence,
		j.lease_expires_at > clock_timestamp(),j.cancellation_requested,
		c.checkpoint_id,c.stage,c.fence,c.payload,c.payload_hash,c.terminal_status
		FROM jobs j JOIN job_checkpoints c ON c.checkpoint_id=j.latest_checkpoint_id
		AND c.job_id=j.job_id WHERE j.job_id=$1 FOR SHARE OF j,c`, proof.JobID).
		Scan(&storedCorpus, &state, &stage, &owner, &fence, &leaseLive, &cancelled,
			&checkpointID, &checkpointStage, &checkpointFence, &payload, &payloadHash, &terminal)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("RESOLVE lease or source checkpoint is absent: %w", ErrStaleFence)
	}
	if err != nil {
		return fmt.Errorf("read RESOLVE lease and source checkpoint: %w", err)
	}
	if storedCorpus != corpusID || state != int16(pb.JobState_JOB_STATE_RUNNING) ||
		stage != int16(pb.JobStage_JOB_STAGE_RESOLVE) || owner != proof.OwnerID ||
		fence != int64(proof.Fence) || !leaseLive || cancelled ||
		checkpointID != proof.SourceCheckpointID {
		return fmt.Errorf("RESOLVE lease changed or expired: %w", ErrStaleFence)
	}
	if checkpointStage != int16(pb.JobStage_JOB_STAGE_EXTRACT) ||
		checkpointFence <= 0 || checkpointFence >= fence ||
		!terminal.Valid || terminal.Int16 != int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) {
		return fmt.Errorf("RESOLVE source is not a successful EXTRACT checkpoint: %w", domain.ErrPersistentIntegrity)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != payloadHash {
		return fmt.Errorf("EXTRACT checkpoint payload checksum differs: %w", domain.ErrPersistentIntegrity)
	}
	checkpoint := new(pb.Checkpoint)
	if err = proto.Unmarshal(payload, checkpoint); err != nil {
		return fmt.Errorf("decode EXTRACT checkpoint: %w", domain.ErrPersistentIntegrity)
	}
	if err = domain.ValidateWire(checkpoint, domain.DefaultWireLimits); err != nil {
		return fmt.Errorf("validate EXTRACT checkpoint: %w", domain.ErrPersistentIntegrity)
	}
	if checkpoint.GetMeta().GetRecordId() != checkpointID ||
		checkpoint.GetMeta().GetCorpusId() != corpusID || checkpoint.JobId != proof.JobID ||
		checkpoint.Stage != pb.JobStage_JOB_STAGE_EXTRACT ||
		checkpoint.Fence != uint64(checkpointFence) ||
		checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 ||
		checkpoint.CompletedBatchKeys[0] != sourceRef.ArtifactId ||
		!proto.Equal(checkpoint.Manifest, source.GetDependencies().GetProducerManifest()) ||
		!proto.Equal(checkpoint.ArtifactHashes[0], sourceRef.ContentHash) {
		return fmt.Errorf("EXTRACT checkpoint differs from source artifact: %w", domain.ErrPersistentIntegrity)
	}
	return nil
}

func verifySemanticLeaseStillLive(ctx context.Context, tx pgx.Tx, proof SemanticJobFence) error {
	var live bool
	err := tx.QueryRow(ctx, `SELECT lease_expires_at > clock_timestamp() AND NOT cancellation_requested
		FROM jobs WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3 AND
		state=$4 AND stage=$5`, proof.JobID, proof.OwnerID, int64(proof.Fence),
		int16(pb.JobState_JOB_STATE_RUNNING), int16(pb.JobStage_JOB_STAGE_RESOLVE)).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !live {
		return fmt.Errorf("RESOLVE lease expired before commit: %w", ErrStaleFence)
	}
	if err != nil {
		return fmt.Errorf("recheck RESOLVE lease: %w", err)
	}
	return nil
}
