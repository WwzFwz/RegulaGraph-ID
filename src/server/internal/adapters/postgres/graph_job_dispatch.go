// Rechecks a preflight-admitted graph job immediately before worker dispatch.
// One SQL statement binds the immutable inventory to the live child/source leases,
// publication, registry stamp and base reader pin. It returns an owned assignment,
// never advances the job or authorizes output publication. The caller must recheck
// authority when committing output; no read can reserve it across a remote RPC.
// Cache admission only until its stamp changes. Measure SQL/pool p95 and reject
// rates against configs/benchmark-targets.yaml; no model is called here.
package postgres

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (admission *GraphJobAdmission) AuthorizeGraphDispatch(ctx context.Context, pin domain.SnapshotPin, job domain.JobRecord) (domain.GraphJobAssignment, error) {
	var empty domain.GraphJobAssignment
	if ctx == nil || admission == nil || admission.repository == nil {
		return empty, errors.New("verified graph admission required")
	}
	if err := validateIndexPin(pin); err != nil {
		return empty, err
	}
	if job.State != pb.JobState_JOB_STATE_RUNNING || job.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE ||
		job.CancellationRequested || job.Attempt == 0 || job.LeaseFence == 0 || job.LeaseFence > math.MaxInt64 ||
		!storageIDPattern.MatchString(job.LeaseOwner) || !job.LeaseExpiresAt.After(time.Now()) {
		return empty, ErrStaleFence
	}
	position := -1
	for i, assignment := range admission.inventory.Assignments {
		if assignment.JobID == job.JobID {
			position = i
			break
		}
	}
	if position < 0 {
		return empty, ErrConflict
	}
	a := admission.inventory.Assignments[position]
	p := a.Plan
	if job.CorpusID != p.Meta.CorpusId || pin.CorpusID != job.CorpusID || pin.SnapshotID != p.Context.SnapshotRef.SnapshotId || pin.Sequence != p.Context.SnapshotRef.Sequence {
		return empty, ErrConflict
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	var valid bool
	err := admission.repository.pool.QueryRow(bounded, `SELECT EXISTS (
 SELECT 1 FROM graph_job_assignments a
 JOIN graph_job_inventories i ON i.publication_id=a.publication_id
 JOIN jobs j ON j.job_id=a.job_id
 JOIN jobs source ON source.job_id=a.source_job_id
 JOIN job_checkpoints cp ON cp.checkpoint_id=source.latest_checkpoint_id AND cp.job_id=source.job_id
 JOIN snapshots s ON s.publication_id=i.publication_id AND s.corpus_id=i.corpus_id
 JOIN corpus_state c ON c.corpus_id=i.corpus_id
 JOIN snapshots b ON b.snapshot_id=s.parent_snapshot_id AND b.corpus_id=i.corpus_id
 JOIN snapshot_registry_bindings v ON v.publication_id=s.publication_id AND v.corpus_id=i.corpus_id
 JOIN snapshot_read_leases lease ON lease.snapshot_id=b.snapshot_id AND lease.corpus_id=i.corpus_id
 WHERE i.publication_id=@publication AND i.corpus_id=@corpus AND i.payload_hash=@digest
 AND a.job_id=@job AND a.source_job_id=@source AND a.plan_artifact_id=@plan AND a.ordinal=@ordinal
 AND j.corpus_id=@corpus AND j.lease_owner=@owner AND j.lease_fence=@job_fence AND j.attempt=@attempt
 AND j.lease_expires_at>=@expires AND j.lease_expires_at>clock_timestamp()
 AND j.state=@running AND j.stage=@assemble AND NOT j.cancellation_requested
 AND source.corpus_id=@corpus AND NOT source.cancellation_requested AND source.stage=@resolve
 AND source.state IN (@staged,@succeeded) AND source.latest_checkpoint_id=@checkpoint
 AND cp.fence=source.lease_fence AND cp.stage=@resolve AND cp.terminal_status=@complete
 AND s.fence=@fence AND i.fence=s.fence AND c.publisher_fence=s.fence
 AND s.sequence=@sequence AND s.state IN (@staging,@validating)
 AND c.active_snapshot_id=b.snapshot_id AND b.state=@published AND b.snapshot_id=@base
 AND b.sequence=@base_sequence AND b.manifest_hash=@manifest AND b.representation_generation=@generation
 AND v.fence=s.fence AND v.registry_revision=@view_revision
 AND c.registry_revision=@revision AND c.registry_history_floor=@floor
 AND lease.lease_id=@lease AND lease.owner_id=@pin_owner AND lease.expires_at=@pin_expires
 AND lease.expires_at>clock_timestamp())`, pgx.NamedArgs{
		"publication": p.PublicationId, "corpus": job.CorpusID, "digest": admission.digest,
		"job": job.JobID, "source": a.SourceJobID, "plan": a.Reference.ArtifactId, "ordinal": position,
		"owner": job.LeaseOwner, "job_fence": int64(job.LeaseFence), "attempt": int64(job.Attempt), "expires": job.LeaseExpiresAt,
		"running": int16(pb.JobState_JOB_STATE_RUNNING), "assemble": int16(pb.JobStage_JOB_STAGE_ASSEMBLE),
		"resolve": int16(pb.JobStage_JOB_STAGE_RESOLVE), "staged": int16(pb.JobState_JOB_STATE_STAGED),
		"succeeded": int16(pb.JobState_JOB_STATE_SUCCEEDED), "checkpoint": p.SourceCheckpointId,
		"complete": int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED), "fence": int64(p.PublicationFence), "sequence": int64(p.TargetSequence),
		"staging": int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING), "validating": int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING),
		"published": int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED), "base": pin.SnapshotID, "base_sequence": int64(pin.Sequence),
		"manifest": p.Context.SnapshotRef.ManifestHash.Sha256, "generation": p.Context.SnapshotRef.RepresentationGeneration,
		"view_revision": int64(p.RegistryRevision), "revision": admission.revision, "floor": admission.historyFloor,
		"lease": pin.LeaseID, "pin_owner": pin.OwnerID, "pin_expires": pin.ExpiresAt,
	}).Scan(&valid)
	if err != nil {
		return empty, err
	}
	if !valid {
		return empty, ErrConflict
	}
	return domain.GraphJobAssignment{JobID: a.JobID, SourceJobID: a.SourceJobID,
		Plan: proto.Clone(p).(*pb.GraphAssemblyPlan), Reference: proto.Clone(a.Reference).(*pb.ArtifactRef)}, nil
}
