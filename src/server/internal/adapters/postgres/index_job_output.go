// Commits an already byte-verified INDEX output to its exact durable assignment.
// Checkpoint and STAGED transition share one transaction with publication fence,
// source cancellation/checkpoint and registered output checks. The caller must
// authenticate output bytes and validate the complete source/plan/vector closure
// before calling; this metadata adapter never performs inference or publication.
// Measure commit lock/pool p95/p99 using configs/benchmark-targets.yaml.
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

func (r *Repository) SaveIndexCheckpoint(ctx context.Context, cp *pb.Checkpoint, owner string, ref *pb.ArtifactRef, batch *pb.IndexBatch) error {
	for _, message := range []proto.Message{cp, ref} {
		if err := domain.ValidateWire(message, domain.DefaultWireLimits); err != nil {
			return err
		}
	}
	// Match the existing INDEX admission budget: dense values count as wire
	// items, so normal 128 x 1024 embedding batches exceed the metadata default.
	limits := domain.DefaultWireLimits
	limits.MaxItems = 1_000_000
	if err := domain.ValidateWire(batch, limits); err != nil {
		return err
	}
	if cp.Stage != pb.JobStage_JOB_STAGE_INDEX || cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
		ref.MediaType != domain.IndexBatchMediaType || batch.Counts.Rejected != 0 || batch.Counts.Accepted != uint64(len(batch.Records)) || batch.Counts.Expected != batch.Counts.Accepted ||
		batch.Meta.CorpusId != cp.Meta.CorpusId || len(cp.CompletedBatchKeys) != 1 || len(cp.ArtifactHashes) != 1 ||
		cp.CompletedBatchKeys[0] != ref.ArtifactId || !proto.Equal(cp.ArtifactHashes[0], ref.ContentHash) {
		return errors.New("INDEX checkpoint requires one complete matching output")
	}
	return r.saveCheckpoint(ctx, cp, owner, func(ctx context.Context, tx pgx.Tx) error {
		var sourceJob, publication, scope string
		var fence int64
		var planBytes, planRefBytes, snapshotBytes []byte
		err := tx.QueryRow(ctx, `SELECT a.source_job_id,a.publication_id,a.plan_payload,a.plan_reference,i.fence,i.auth_scope,i.snapshot_payload
    FROM index_job_assignments a JOIN index_job_inventories i ON i.publication_id=a.publication_id
    WHERE a.job_id=$1 AND i.corpus_id=$2`, cp.JobId, cp.Meta.CorpusId).Scan(&sourceJob, &publication, &planBytes, &planRefBytes, &fence, &scope, &snapshotBytes)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		plan, planRef, snapshot := new(pb.IndexBuildPlan), new(pb.ArtifactRef), new(pb.SnapshotRef)
		for _, pair := range []struct {
			raw    []byte
			target proto.Message
		}{{planBytes, plan}, {planRefBytes, planRef}, {snapshotBytes, snapshot}} {
			if err = domain.DecodeWire(pair.raw, pair.target, domain.DefaultWireLimits); err != nil {
				return err
			}
		}
		if err = domain.ValidateIndexBuildPlan(plan); err != nil {
			return err
		}
		if uint64(len(planBytes)) != planRef.ByteSize || fmt.Sprintf("%x", sha256.Sum256(planBytes)) != planRef.ContentHash.Sha256 {
			return domain.ErrPersistentIntegrity
		}
		if batch.Meta.RecordId != plan.OutputBatchId || !proto.Equal(batch.BuildPlan, planRef) || !proto.Equal(batch.Generation, plan.Generation) ||
			!proto.Equal(cp.Manifest, plan.Producer) || batch.Context.AuthScopeRef != scope || !proto.Equal(batch.Context.SnapshotRef, snapshot) ||
			len(batch.Records) != len(plan.Items) || len(batch.Closures) != 0 {
			return domain.ErrPersistentIntegrity
		}
		for i, item := range plan.Items {
			if batch.Records[i].Meta.RecordId != item.RecordId || batch.Records[i].ChunkId != item.ChunkId {
				return domain.ErrPersistentIntegrity
			}
		}
		binding := domain.IndexCatalogBinding{PublicationID: publication, Fence: uint64(fence), Generation: plan.Generation}
		if _, err = lockIndexPublication(ctx, tx, binding); err != nil {
			return err
		}
		var cancelled bool
		if err = tx.QueryRow(ctx, `SELECT cancellation_requested FROM jobs WHERE job_id=$1 FOR SHARE`, sourceJob).Scan(&cancelled); err != nil {
			return err
		}
		if cancelled {
			return ErrConflict
		}
		if err = verifyIndexSourceCheckpoint(ctx, tx, cp.Meta.CorpusId, sourceJob, plan.DocumentBatch); err != nil {
			return err
		}
		registered, err := loadArtifact(ctx, tx, cp.Meta.CorpusId, ref.ArtifactId)
		if err != nil {
			return err
		}
		if !proto.Equal(registered, ref) {
			return domain.ErrPersistentIntegrity
		}
		tag, err := tx.Exec(ctx, `UPDATE jobs SET state=$4,updated_at=clock_timestamp(),lease_owner=NULL,lease_expires_at=NULL
		WHERE job_id=$1 AND lease_owner=$2 AND lease_fence=$3 AND NOT cancellation_requested AND stage=$5
		AND lease_expires_at >= clock_timestamp()`, cp.JobId, owner, int64(cp.Fence), int16(pb.JobState_JOB_STATE_STAGED), int16(pb.JobStage_JOB_STAGE_INDEX))
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrStaleFence
		}
		return nil
	})
}
