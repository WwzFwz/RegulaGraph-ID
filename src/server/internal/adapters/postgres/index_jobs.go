// Atomically persists a frozen initial INDEX inventory and one durable child job
// per plan. Replay must preserve every job/source/plan/snapshot/binding; no subset
// can replace the inventory. Source checkpoints and registered plans are checked
// under publication/source locks. Outputs remain worker proposals until fenced
// checkpoint and publication stages complete. Measure lock time, scheduling RSS,
// retries and p95 under configs/benchmark-targets.yaml; acceptance is unmeasured.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) ScheduleIndexJobs(ctx context.Context, inventory domain.IndexJobInventory) error {
	if err := domain.ValidateIndexJobInventory(inventory); err != nil {
		return err
	}
	// The generation registration is independently idempotent; a later failure
	// may leave this immutable prerequisite, but never a partial job inventory.
	if err := r.RegisterIndexGeneration(ctx, inventory.Binding); err != nil {
		return err
	}
	canonical, err := json.Marshal(inventory)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(canonical))
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	sequence, err := lockIndexPublication(ctx, tx, inventory.Binding)
	if err != nil {
		return err
	}
	var snapshotID string
	if err = tx.QueryRow(ctx, `SELECT snapshot_id FROM snapshots WHERE publication_id=$1`, inventory.Binding.PublicationID).Scan(&snapshotID); err != nil {
		return err
	}
	if snapshotID != inventory.Snapshot.SnapshotId || sequence != inventory.Snapshot.Sequence {
		return fmt.Errorf("INDEX inventory snapshot differs from reservation: %w", ErrConflict)
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT inventory_hash FROM index_job_inventories WHERE publication_id=$1`, inventory.Binding.PublicationID).Scan(&existing)
	if err == nil {
		if existing != digest {
			return fmt.Errorf("INDEX inventory drift: %w", ErrConflict)
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// Acquire source row locks in deterministic order. Cancellation/checkpoint
	// commits cannot cross source validation and atomic child creation.
	sourceIDs := map[string]bool{}
	for _, assignment := range inventory.Assignments {
		sourceIDs[assignment.SourceJobID] = true
	}
	ordered := make([]string, 0, len(sourceIDs))
	for id := range sourceIDs {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	requests := map[string]*pb.IngestionRequest{}
	for _, id := range ordered {
		var raw []byte
		var hash string
		err = tx.QueryRow(ctx, `SELECT request_payload,request_hash FROM jobs WHERE job_id=$1 AND corpus_id=$2 AND NOT cancellation_requested FOR SHARE`, id, inventory.Snapshot.CorpusId).Scan(&raw, &hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("INDEX source job missing/cancelled: %w", ErrConflict)
		}
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != hash {
			return domain.ErrPersistentIntegrity
		}
		request := new(pb.IngestionRequest)
		if err = domain.DecodeWire(raw, request, domain.DefaultWireLimits); err != nil {
			return err
		}
		if request.CorpusId != inventory.Snapshot.CorpusId {
			return domain.ErrPersistentIntegrity
		}
		requests[id] = request
	}
	snapshotBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(inventory.Snapshot)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO index_job_inventories(publication_id,corpus_id,fence,auth_scope,snapshot_payload,inventory_hash,job_count) VALUES($1,$2,$3,$4,$5,$6,$7)`, inventory.Binding.PublicationID, inventory.Snapshot.CorpusId, int64(inventory.Binding.Fence), inventory.AuthScope, snapshotBytes, digest, len(inventory.Assignments))
	if err != nil {
		return err
	}
	for ordinal, a := range inventory.Assignments {
		if err = verifyIndexSourceCheckpoint(ctx, tx, inventory.Snapshot.CorpusId, a.SourceJobID, a.Plan.DocumentBatch); err != nil {
			return err
		}
		registered, e := loadArtifact(ctx, tx, inventory.Snapshot.CorpusId, a.Reference.ArtifactId)
		if e != nil {
			return e
		}
		if !proto.Equal(registered, a.Reference) {
			return domain.ErrPersistentIntegrity
		}
		request := proto.Clone(requests[a.SourceJobID]).(*pb.IngestionRequest)
		request.Operation = pb.JobOperation_JOB_OPERATION_REBUILD
		request.IdempotencyKey = a.JobID
		request.ConfigManifest = proto.Clone(a.Plan.Producer).(*pb.ProducerManifest)
		if err = domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
			return err
		}
		payload, e := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
		if e != nil {
			return e
		}
		hash := sha256.Sum256(payload)
		request.IdempotencyKey = ""
		semantic, e := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
		if e != nil {
			return e
		}
		fingerprint := sha256.Sum256(semantic)
		_, err = tx.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload) VALUES($1,$2,$3,$4,$5,$6,$1,$7,$8)`, a.JobID, inventory.Snapshot.CorpusId, int16(pb.JobOperation_JOB_OPERATION_REBUILD), int16(pb.JobState_JOB_STATE_QUEUED), int16(pb.JobStage_JOB_STAGE_INDEX), hex.EncodeToString(fingerprint[:]), hex.EncodeToString(hash[:]), payload)
		if err != nil {
			return err
		}
		planBytes, e := (proto.MarshalOptions{Deterministic: true}).Marshal(a.Plan)
		if e != nil {
			return e
		}
		refBytes, e := (proto.MarshalOptions{Deterministic: true}).Marshal(a.Reference)
		if e != nil {
			return e
		}
		_, err = tx.Exec(ctx, `INSERT INTO index_job_assignments(job_id,publication_id,source_job_id,ordinal,plan_payload,plan_reference) VALUES($1,$2,$3,$4,$5,$6)`, a.JobID, inventory.Binding.PublicationID, a.SourceJobID, ordinal, planBytes, refBytes)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) LoadIndexJobInventory(ctx context.Context, publicationID string) (domain.IndexJobInventory, error) {
	var result domain.IndexJobInventory
	var snapshotBytes []byte
	var corpus, digest string
	var fence int64
	var expected int
	err := r.pool.QueryRow(ctx, `SELECT corpus_id,fence,auth_scope,snapshot_payload,inventory_hash,job_count FROM index_job_inventories WHERE publication_id=$1`, publicationID).Scan(&corpus, &fence, &result.AuthScope, &snapshotBytes, &digest, &expected)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	result.Snapshot = new(pb.SnapshotRef)
	if err = domain.DecodeWire(snapshotBytes, result.Snapshot, domain.DefaultWireLimits); err != nil {
		return result, err
	}
	result.Binding, err = r.LoadIndexGeneration(ctx, corpus, result.Snapshot.RepresentationGeneration)
	if err != nil {
		return result, err
	}
	if result.Binding.PublicationID != publicationID || result.Binding.Fence != uint64(fence) {
		return result, domain.ErrPersistentIntegrity
	}
	rows, err := r.pool.Query(ctx, `SELECT job_id,source_job_id,ordinal,plan_payload,plan_reference FROM index_job_assignments WHERE publication_id=$1 ORDER BY ordinal LIMIT 257`, publicationID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.IndexJobAssignment
		var ordinal int
		var plan, ref []byte
		if err = rows.Scan(&a.JobID, &a.SourceJobID, &ordinal, &plan, &ref); err != nil {
			return result, err
		}
		if ordinal != len(result.Assignments) || len(result.Assignments) >= 256 {
			return result, domain.ErrPersistentIntegrity
		}
		a.Plan = new(pb.IndexBuildPlan)
		a.Reference = new(pb.ArtifactRef)
		if err = domain.DecodeWire(plan, a.Plan, domain.DefaultWireLimits); err != nil {
			return result, err
		}
		if err = domain.DecodeWire(ref, a.Reference, domain.DefaultWireLimits); err != nil {
			return result, err
		}
		result.Assignments = append(result.Assignments, a)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(result.Assignments) != expected {
		return result, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateIndexJobInventory(result); err != nil {
		return result, err
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(canonical)) != digest {
		return result, domain.ErrPersistentIntegrity
	}
	return result, nil
}

// IndexJobPublication is a cheap pre-dispatch authority check even when the
// immutable admitted inventory is cached. Output commit checks authority again.
func (r *Repository) IndexJobPublication(ctx context.Context, job domain.JobRecord) (string, error) {
	var publication string
	err := r.pool.QueryRow(ctx, `SELECT i.publication_id FROM index_job_assignments a
	 JOIN index_job_inventories i ON i.publication_id=a.publication_id
	 JOIN jobs j ON j.job_id=a.job_id JOIN jobs source ON source.job_id=a.source_job_id
	 JOIN snapshots s ON s.publication_id=i.publication_id
	 JOIN corpus_state c ON c.corpus_id=i.corpus_id
	 WHERE j.job_id=$1 AND j.corpus_id=$2 AND i.corpus_id=$2 AND j.lease_owner=$3 AND j.lease_fence=$4
	 AND j.stage=$5 AND j.state=$6 AND j.lease_expires_at>=clock_timestamp()
	 AND NOT j.cancellation_requested AND NOT source.cancellation_requested
	 AND s.fence=i.fence AND c.publisher_fence=i.fence AND s.state IN ($7,$8)`,
		job.JobID, job.CorpusID, job.LeaseOwner, int64(job.LeaseFence), int16(pb.JobStage_JOB_STAGE_INDEX), int16(pb.JobState_JOB_STATE_RUNNING),
		int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING), int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING)).Scan(&publication)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrIndexReplan
	}
	return publication, err
}
