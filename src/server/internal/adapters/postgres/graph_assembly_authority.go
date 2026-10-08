// Checks durable source/publication authority before ASSEMBLE preparation or commit.
// These read gates authenticate registered RESOLVE output/checkpoint and an open
// publication's parent, sequence, registry revision and fence. They do not reserve
// a lease across calls; scheduling/output transactions must repeat these predicates
// while holding their own locks. Caller authenticates corpus access and bytes.
// Measure SQL p95/p99, admission races and pool wait under required benchmark targets.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// VerifyGraphAssemblyPublication reads all mutable publication fields in one
// statement. Only a published active base can be extended; no guessed bootstrap.
func (r *Repository) VerifyGraphAssemblyPublication(ctx context.Context, plan *pb.GraphAssemblyPlan) error {
	if err := domain.ValidateGraphAssemblyPlan(plan); err != nil {
		return err
	}
	base := plan.Context.SnapshotRef
	var valid bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (
 SELECT 1 FROM snapshots s
 JOIN corpus_state c ON c.corpus_id=s.corpus_id
 JOIN snapshots b ON b.snapshot_id=s.parent_snapshot_id AND b.corpus_id=s.corpus_id
 JOIN snapshot_registry_bindings v ON v.publication_id=s.publication_id AND v.corpus_id=s.corpus_id
 WHERE s.publication_id=$1 AND s.corpus_id=$2 AND s.fence=$3 AND c.publisher_fence=s.fence
 AND s.sequence=$4 AND s.state IN ($5,$6) AND b.snapshot_id=$7
 AND c.active_snapshot_id=b.snapshot_id AND b.state=$8 AND b.sequence=$9
 AND b.manifest_hash=$10 AND b.representation_generation=$11
 AND v.fence=s.fence AND v.registry_revision=$12 AND v.registry_revision>=c.registry_history_floor
 AND v.registry_revision<=c.registry_revision)`, plan.PublicationId, plan.Meta.CorpusId, int64(plan.PublicationFence),
		int64(plan.TargetSequence), int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING), int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING),
		base.SnapshotId, int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED), int64(base.Sequence), base.ManifestHash.Sha256,
		base.RepresentationGeneration, int64(plan.RegistryRevision)).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}

func (r *Repository) VerifyGraphAssemblySourceCheckpoint(ctx context.Context, corpus, job, checkpointID string, ref *pb.ArtifactRef) error {
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(job) || !storageIDPattern.MatchString(checkpointID) || ref == nil {
		return errors.New("complete ASSEMBLE source checkpoint required")
	}
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return err
	}
	if ref.SchemaVersion != 1 || ref.MediaType != "application/x-protobuf" && ref.MediaType != "application/x-protobuf; message=regulagraph.v1.ResolutionBatch" {
		return errors.New("typed RESOLVE artifact required")
	}
	registered, err := r.LoadArtifact(ctx, corpus, ref.ArtifactId)
	if err != nil {
		return err
	}
	if !proto.Equal(registered, ref) {
		return domain.ErrPersistentIntegrity
	}
	var raw []byte
	var digest, storedID string
	var fence int64
	var terminal int16
	err = r.pool.QueryRow(ctx, `SELECT c.payload,c.payload_hash,c.checkpoint_id,c.fence,COALESCE(c.terminal_status,0)
 FROM jobs j JOIN job_checkpoints c ON c.checkpoint_id=j.latest_checkpoint_id AND c.job_id=j.job_id
 WHERE j.job_id=$1 AND j.corpus_id=$2 AND NOT j.cancellation_requested
 AND j.stage=$3 AND c.stage=$3 AND c.fence=j.lease_fence AND j.state IN ($4,$5)`, job, corpus, int16(pb.JobStage_JOB_STAGE_RESOLVE),
		int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobState_JOB_STATE_SUCCEEDED)).Scan(&raw, &digest, &storedID, &fence, &terminal)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != digest {
		return domain.ErrPersistentIntegrity
	}
	checkpoint := new(pb.Checkpoint)
	if err = domain.DecodeWire(raw, checkpoint, domain.DefaultWireLimits); err != nil {
		return domain.ErrPersistentIntegrity
	}
	if storedID != checkpointID || checkpoint.Meta.SchemaVersion != 1 || checkpoint.Meta.Visibility != nil || checkpoint.Meta.RecordId != checkpointID || checkpoint.Meta.CorpusId != corpus || checkpoint.JobId != job ||
		checkpoint.Fence != uint64(fence) || terminal != int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) ||
		checkpoint.Stage != pb.JobStage_JOB_STAGE_RESOLVE || checkpoint.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		len(checkpoint.CompletedBatchKeys) != 1 || len(checkpoint.ArtifactHashes) != 1 || checkpoint.CompletedBatchKeys[0] != ref.ArtifactId || !proto.Equal(checkpoint.ArtifactHashes[0], ref.ContentHash) {
		return domain.ErrPersistentIntegrity
	}
	return nil
}
