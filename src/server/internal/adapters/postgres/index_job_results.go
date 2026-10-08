// Collects the complete committed output set of one immutable INDEX inventory.
// A single bounded statement observes all child checkpoints consistently; missing,
// cancelled or unfinished children never yield a partial successful result.
// Metadata/checkpoint integrity is checked here; indexing must still authenticate
// bytes and re-admit every output against the full plan set before backend writes.
// Measure collection latency/RSS and pool time against benchmark-targets.yaml;
// STAGED results prove neither search readiness nor model quality.
package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// ErrIndexOutputsPending indicates that the inventory exists but some child has
// no usable committed result. Failed/cancelled jobs require operator inspection;
// this error does not request automatic rerunning or hide their durable state.
var ErrIndexOutputsPending = errors.New("INDEX inventory has unfinished or cancelled children")

func (r *Repository) LoadIndexJobOutputs(ctx context.Context, publication string) ([]*pb.ArtifactRef, error) {
	inventory, err := r.LoadIndexJobInventory(ctx, publication)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT a.job_id,a.ordinal,j.state,j.stage,j.cancellation_requested,j.lease_fence,
	 COALESCE(c.payload,''::bytea),COALESCE(c.payload_hash,''),COALESCE(c.checkpoint_id,''),
	 COALESCE(c.job_id,''),COALESCE(c.fence,0),COALESCE(c.stage,0),COALESCE(c.terminal_status,0)
	 FROM index_job_assignments a JOIN jobs j ON j.job_id=a.job_id
	 LEFT JOIN job_checkpoints c ON c.checkpoint_id=j.latest_checkpoint_id
	 WHERE a.publication_id=$1 ORDER BY a.ordinal LIMIT 257`, publication)
	if err != nil {
		return nil, err
	}
	// Release rows before registry lookups: this also works with a one-connection pool.
	checkpoints := []*pb.Checkpoint{}
	pending := false
	for rows.Next() {
		var job, hash, checkpointID, checkpointJob string
		var ordinal int
		var state, stage, checkpointStage, terminal int16
		var fence, checkpointFence int64
		var cancelled bool
		var raw []byte
		if err = rows.Scan(&job, &ordinal, &state, &stage, &cancelled, &fence, &raw, &hash, &checkpointID, &checkpointJob, &checkpointFence, &checkpointStage, &terminal); err != nil {
			break
		}
		if ordinal != len(checkpoints) || ordinal >= len(inventory.Assignments) || job != inventory.Assignments[ordinal].JobID {
			err = domain.ErrPersistentIntegrity
			break
		}
		if state != int16(pb.JobState_JOB_STATE_STAGED) || cancelled {
			pending = true
			checkpoints = append(checkpoints, nil)
			continue
		}
		cp := new(pb.Checkpoint)
		if hash != fmt.Sprintf("%x", sha256.Sum256(raw)) {
			err = domain.ErrPersistentIntegrity
			break
		}
		if err = domain.DecodeWire(raw, cp, domain.DefaultWireLimits); err != nil {
			break
		}
		if stage != int16(pb.JobStage_JOB_STAGE_INDEX) || checkpointStage != stage || checkpointJob != job || checkpointFence != fence ||
			terminal != int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) || cp.Stage != pb.JobStage_JOB_STAGE_INDEX || cp.JobId != job ||
			cp.Meta.RecordId != checkpointID || cp.Meta.CorpusId != inventory.Snapshot.CorpusId || cp.Fence != uint64(fence) ||
			cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || len(cp.CompletedBatchKeys) != 1 || len(cp.ArtifactHashes) != 1 ||
			!proto.Equal(cp.Manifest, inventory.Assignments[ordinal].Plan.Producer) {
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
		return nil, err
	}
	if len(checkpoints) != len(inventory.Assignments) {
		return nil, domain.ErrPersistentIntegrity
	}
	if pending {
		return nil, ErrIndexOutputsPending
	}
	refs := make([]*pb.ArtifactRef, 0, len(checkpoints))
	seen := map[string]bool{}
	for _, cp := range checkpoints {
		ref, e := r.LoadArtifact(ctx, inventory.Snapshot.CorpusId, cp.CompletedBatchKeys[0])
		if e != nil {
			return nil, e
		}
		if ref.MediaType != domain.IndexBatchMediaType || !proto.Equal(ref.ContentHash, cp.ArtifactHashes[0]) || seen[ref.ArtifactId] {
			return nil, domain.ErrPersistentIntegrity
		}
		seen[ref.ArtifactId] = true
		refs = append(refs, ref)
	}
	return refs, nil
}
