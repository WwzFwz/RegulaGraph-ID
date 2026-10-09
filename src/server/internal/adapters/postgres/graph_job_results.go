// Collects all committed graph outputs under publication, registry, job and pin
// locks using an existing source-admitted inventory. It never returns a successful
// partial set. Source cancellation, checkpoint drift and registry movement require
// fresh work/admission, not silent reuse. Blob bytes must still be authenticated and
// projected against source evidence by the workflow. No backend write or publication
// happens here. SQL caps checkpoint transfer at 64MiB; use one connection, measure
// lock/collection p95/RSS under benchmark-targets.yaml, and recheck before activation.
package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

var ErrGraphOutputsPending = errors.New("ASSEMBLE inventory has unfinished or cancelled children")

func (admission *GraphJobAdmission) ReadCompletedGraph(ctx context.Context, pin domain.SnapshotPin) (domain.CompletedGraphInventory, error) {
	return admission.withCompletedGraph(ctx, pin, nil)
}

// Internal callback runs after full authority collection and before commit. It
// must use this transaction, never acquire another pool connection or do RPC.
func (admission *GraphJobAdmission) withCompletedGraph(ctx context.Context, pin domain.SnapshotPin, apply func(context.Context, pgx.Tx, domain.CompletedGraphInventory) error) (domain.CompletedGraphInventory, error) {
	var out domain.CompletedGraphInventory
	if ctx == nil || admission == nil || admission.repository == nil || len(admission.inventory.Assignments) == 0 {
		return out, errors.New("source-admitted graph inventory required")
	}
	if err := validateIndexPin(pin); err != nil {
		return out, err
	}
	first := admission.inventory.Assignments[0].Plan
	if pin.CorpusID != first.Meta.CorpusId || pin.SnapshotID != first.Context.SnapshotRef.SnapshotId || pin.Sequence != first.Context.SnapshotRef.Sequence {
		return out, ErrConflict
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	tx, err := admission.repository.pool.Begin(bounded)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(bounded)
	lockMode := " FOR SHARE"
	if apply != nil {
		lockMode = " FOR UPDATE"
	}
	var id, digest string
	var revision, floor, fence int64
	var count int
	if err = tx.QueryRow(bounded, `SELECT publication_id FROM snapshots WHERE publication_id=$1 AND corpus_id=$2`+lockMode, first.PublicationId, pin.CorpusID).Scan(&id); err != nil {
		return out, err
	}
	if err = tx.QueryRow(bounded, `SELECT registry_revision,registry_history_floor FROM corpus_state WHERE corpus_id=$1 FOR SHARE`, pin.CorpusID).Scan(&revision, &floor); err != nil {
		return out, err
	}
	if revision != admission.revision || floor != admission.historyFloor {
		return out, ErrConflict
	}
	if err = verifyGraphPublicationBinding(bounded, tx, first.PublicationId, pin.CorpusID, first.PublicationFence, first.TargetSequence, first.RegistryRevision, first.Context.SnapshotRef); err != nil {
		return out, err
	}
	if err = tx.QueryRow(bounded, `SELECT payload_hash,job_count,fence FROM graph_job_inventories WHERE publication_id=$1 AND corpus_id=$2`, first.PublicationId, pin.CorpusID).Scan(&digest, &count, &fence); err != nil {
		return out, err
	}
	if digest != admission.digest || count != len(admission.inventory.Assignments) || uint64(fence) != first.PublicationFence {
		return out, domain.ErrPersistentIntegrity
	}
	jobs := make([]string, 0, count*2)
	for _, a := range admission.inventory.Assignments {
		jobs = append(jobs, a.JobID, a.SourceJobID)
	}
	locked, err := tx.Query(bounded, `SELECT job_id FROM jobs WHERE job_id=ANY($1::text[]) AND corpus_id=$2 ORDER BY job_id FOR SHARE`, jobs, pin.CorpusID)
	if err != nil {
		return out, err
	}
	lockedCount := 0
	for locked.Next() {
		lockedCount++
	}
	err = locked.Err()
	locked.Close()
	if err != nil {
		return out, err
	}
	if lockedCount != len(jobs) {
		return out, domain.ErrPersistentIntegrity
	}
	// Pin row is held so release cannot cross the final source membership check.
	if err = tx.QueryRow(bounded, `SELECT lease_id FROM snapshot_read_leases WHERE lease_id=$1 FOR SHARE`, pin.LeaseID).Scan(&id); err != nil {
		return out, err
	}
	for i, a := range admission.inventory.Assignments {
		b := admission.bindings[i]
		if err = verifyGraphAssemblySourceCheckpoint(bounded, tx, pin.CorpusID, a.SourceJobID, b.SourceCheckpointID, b.OriginalResolution); err != nil {
			return out, err
		}
		if err = verifyPublishedGraphSourceBinding(bounded, tx, pin, b.Source); err != nil {
			return out, err
		}
	}
	rows, err := tx.Query(bounded, `SELECT a.job_id,a.source_job_id,a.plan_artifact_id,a.ordinal,
 j.state,j.stage,j.cancellation_requested,j.lease_fence,
 CASE WHEN octet_length(c.payload)<=16777216 AND sum(octet_length(c.payload)) OVER ()<=67108864 THEN c.payload ELSE NULL END,
 COALESCE(c.payload_hash,''),COALESCE(c.checkpoint_id,''),COALESCE(c.job_id,''),COALESCE(c.fence,0),COALESCE(c.stage,0),COALESCE(c.terminal_status,0)
 FROM graph_job_assignments a JOIN jobs j ON j.job_id=a.job_id
 LEFT JOIN job_checkpoints c ON c.checkpoint_id=j.latest_checkpoint_id
 WHERE a.publication_id=$1 ORDER BY a.ordinal LIMIT 257`, first.PublicationId)
	if err != nil {
		return out, err
	}
	var checkpoints []*pb.Checkpoint
	pending := false
	for rows.Next() {
		var job, source, planID, hash, checkpointID, checkpointJob string
		var ordinal int
		var state, stage, checkpointStage, terminal int16
		var jobFence, checkpointFence int64
		var cancelled bool
		var raw []byte
		if err = rows.Scan(&job, &source, &planID, &ordinal, &state, &stage, &cancelled, &jobFence, &raw, &hash, &checkpointID, &checkpointJob, &checkpointFence, &checkpointStage, &terminal); err != nil {
			break
		}
		if ordinal != len(checkpoints) || ordinal >= count {
			err = domain.ErrPersistentIntegrity
			break
		}
		a := admission.inventory.Assignments[ordinal]
		if job != a.JobID || source != a.SourceJobID || planID != a.Reference.ArtifactId || stage != int16(pb.JobStage_JOB_STAGE_ASSEMBLE) {
			err = domain.ErrPersistentIntegrity
			break
		}
		if state != int16(pb.JobState_JOB_STATE_STAGED) || cancelled {
			pending = true
			checkpoints = append(checkpoints, nil)
			continue
		}
		cp := new(pb.Checkpoint)
		if len(raw) == 0 || fmt.Sprintf("%x", sha256.Sum256(raw)) != hash || domain.DecodeWire(raw, cp, domain.DefaultWireLimits) != nil ||
			cp.Meta.RecordId != checkpointID || checkpointJob != job || checkpointFence != jobFence || cp.Fence != uint64(jobFence) ||
			checkpointStage != stage || terminal != int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) {
			err = domain.ErrPersistentIntegrity
			break
		}
		checkpoints = append(checkpoints, cp)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(checkpoints) != count {
		return out, domain.ErrPersistentIntegrity
	}
	if pending {
		return out, ErrGraphOutputsPending
	}
	result := domain.CompletedGraphInventory{Checkpoints: checkpoints}
	for i, cp := range checkpoints {
		if len(cp.CompletedBatchKeys) != 1 {
			return out, domain.ErrPersistentIntegrity
		}
		ref, e := loadArtifact(bounded, tx, pin.CorpusID, cp.CompletedBatchKeys[0])
		if e != nil {
			return out, e
		}
		a := admission.inventory.Assignments[i]
		result.Inventory.Assignments = append(result.Inventory.Assignments, domain.GraphJobAssignment{JobID: a.JobID, SourceJobID: a.SourceJobID, Plan: proto.Clone(a.Plan).(*pb.GraphAssemblyPlan), Reference: proto.Clone(a.Reference).(*pb.ArtifactRef)})
		result.Outputs = append(result.Outputs, ref)
	}
	if err = domain.ValidateCompletedGraphInventory(result); err != nil {
		return out, errors.Join(domain.ErrPersistentIntegrity, err)
	}
	if err = checkIndexLease(bounded, tx, pin); err != nil {
		return out, err
	}
	if apply != nil {
		if err = apply(bounded, tx, result); err != nil {
			return out, err
		}
		if err = checkIndexLease(bounded, tx, pin); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(bounded); err != nil {
		return out, err
	}
	return result, nil
}
