// Persists verified ASSEMBLE output metadata, immutable dependencies, checkpoint
// and STAGED in one transaction. The workflow must authenticate bytes and verify
// the full source projection before calling this internal metadata port. Admission
// owns the source/registry proof; locks repeat live authorization after RPC and
// serialize cancellation, publication takeover and registry movement. Blob storage
// precedes this transaction; an orphan blob is not a published graph. Measure lock
// and commit p95/p99 against configs/benchmark-targets.yaml (not yet measured).
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

func (admission *GraphJobAdmission) CommitGraphOutput(ctx context.Context, pin domain.SnapshotPin, job domain.JobRecord,
	request *pb.ProcessBatchRequest, response *pb.ProcessBatchResponse, manifest *pb.DependencyManifest) error {
	if ctx == nil || admission == nil || admission.repository == nil || request == nil || manifest == nil {
		return errors.New("verified graph output and admission required")
	}
	// Validate before cloning/locking. The second authorization below is the one
	// held through commit; this read only rejects stale work without taking locks.
	a, err := admission.AuthorizeGraphDispatch(ctx, pin, job)
	if err != nil {
		return err
	}
	expected, err := domain.BuildGraphAssemblyRequest(a, job, request.Context)
	if err != nil {
		return err
	}
	if !proto.Equal(expected, request) {
		return ErrConflict
	}
	if err = domain.ValidateGraphWorkerEnvelope(request, response, a.Plan); err != nil {
		return err
	}
	if err = domain.ValidateWire(manifest, domain.DefaultWireLimits); err != nil {
		return err
	}
	if manifest.ArtifactId != a.Plan.OutputArtifactId || !proto.Equal(manifest.ProducerManifest, a.Plan.ProducerManifest) || len(manifest.Dependencies) == 0 {
		return ErrConflict
	}
	// The delta keeps its logical owner. Only the DB reverse-dependency owner is
	// mapped to the physical blob locator; never rewrite the persisted delta.
	owned := proto.Clone(manifest).(*pb.DependencyManifest)
	owned.ArtifactId = response.GraphDelta.ArtifactId
	dependencies, err := artifactManifestDependencies(owned.ArtifactId, owned, true)
	if err != nil {
		return err
	}
	deadline := request.Context.Deadline.AsTime()
	if pin.ExpiresAt.Before(deadline) {
		deadline = pin.ExpiresAt
	}
	if job.LeaseExpiresAt.Before(deadline) {
		deadline = job.LeaseExpiresAt
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := admission.repository.pool.Begin(bounded)
	if err != nil {
		return err
	}
	defer tx.Rollback(bounded)
	// Same order as graph inventory/source receipt writers. Do not use the generic
	// checkpoint wrapper here: it takes the child lock before publication locks.
	for _, lock := range []struct {
		sql  string
		args []any
	}{
		{`SELECT publication_id FROM snapshots WHERE publication_id=$1 AND corpus_id=$2 FOR UPDATE`, []any{a.Plan.PublicationId, job.CorpusID}},
		{`SELECT corpus_id FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, []any{job.CorpusID}},
		{`SELECT job_id FROM jobs WHERE job_id=$1 AND corpus_id=$2 FOR SHARE`, []any{a.SourceJobID, job.CorpusID}},
		{`SELECT job_id FROM jobs WHERE job_id=$1 AND corpus_id=$2 FOR UPDATE`, []any{job.JobID, job.CorpusID}},
		{`SELECT lease_id FROM snapshot_read_leases WHERE lease_id=$1 AND corpus_id=$2 FOR SHARE`, []any{pin.LeaseID, job.CorpusID}},
	} {
		var id string
		if err = tx.QueryRow(bounded, lock.sql, lock.args...).Scan(&id); err != nil {
			return err
		}
	}
	if _, err = admission.authorizeGraphDispatch(bounded, tx, pin, job); err != nil {
		return err
	}
	if err = registerArtifactTx(bounded, tx, job.CorpusID, response.GraphDelta); err != nil {
		return err
	}
	if err = saveArtifactDependenciesTx(bounded, tx, job.CorpusID, owned.ArtifactId, dependencies, true); err != nil {
		return err
	}
	if err = saveCheckpointTx(bounded, tx, response.Checkpoint, job.LeaseOwner, nil); err != nil {
		return err
	}
	// Check time-dependent predicates again after writes/any artifact lock wait.
	if _, err = admission.authorizeGraphDispatch(bounded, tx, pin, job); err != nil {
		return err
	}
	tag, err := tx.Exec(bounded, `UPDATE jobs SET state=$4,lease_owner=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
	 WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3 AND NOT cancellation_requested
	 AND state=$5 AND stage=$6 AND lease_expires_at>clock_timestamp()`, job.JobID, job.LeaseOwner, int64(job.LeaseFence),
		int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobState_JOB_STATE_RUNNING), int16(pb.JobStage_JOB_STAGE_ASSEMBLE))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleFence
	}
	return tx.Commit(bounded)
}

// GraphCheckpointCommitted reconciles a lost acknowledgement, not fresh work.
// It accepts only the exact checkpoint still attached to this STAGED graph child.
func (admission *GraphJobAdmission) GraphCheckpointCommitted(ctx context.Context, cp *pb.Checkpoint) (bool, error) {
	if admission == nil || admission.repository == nil {
		return false, errors.New("graph admission required")
	}
	if err := domain.ValidateWire(cp, domain.DefaultWireLimits); err != nil {
		return false, err
	}
	if cp.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE || cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return false, errors.New("successful ASSEMBLE checkpoint required")
	}
	var raw []byte
	var digest string
	err := admission.repository.pool.QueryRow(ctx, `SELECT CASE WHEN octet_length(c.payload)<=16777216 THEN c.payload ELSE NULL END,c.payload_hash
	 FROM jobs j JOIN job_checkpoints c ON c.checkpoint_id=j.latest_checkpoint_id AND c.job_id=j.job_id
	 JOIN graph_job_assignments a ON a.job_id=j.job_id JOIN graph_job_inventories i ON i.publication_id=a.publication_id
	 WHERE j.job_id=$1 AND j.corpus_id=$2 AND j.state=$3 AND j.stage=$4 AND j.lease_fence=$5
	 AND i.payload_hash=$6 AND i.corpus_id=j.corpus_id`, cp.JobId, cp.Meta.CorpusId, int16(pb.JobState_JOB_STATE_STAGED),
		int16(pb.JobStage_JOB_STAGE_ASSEMBLE), int64(cp.Fence), admission.digest).Scan(&raw, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(raw) == 0 || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
		return false, domain.ErrPersistentIntegrity
	}
	expected, err := (proto.MarshalOptions{Deterministic: true}).Marshal(cp)
	if err != nil {
		return false, err
	}
	return digest == fmt.Sprintf("%x", sha256.Sum256(expected)), nil
}
