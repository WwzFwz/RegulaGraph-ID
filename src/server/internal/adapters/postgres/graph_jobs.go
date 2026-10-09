// Creates all verified ASSEMBLE children and their immutable inventory in one
// transaction. The opaque preflight admission owns plans, historical evidence and
// an optimistic registry stamp; corpus revision/floor, publication, source jobs,
// registered plans and live base membership are checked under ordered locks.
// Registry movement asks for a fresh admission, never a weakened graph view.
// Exact replay creates no duplicates. Measure lock time, retries, queue latency
// and inventory RSS against configs/benchmark-targets.yaml (unmeasured).
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (admission *GraphJobAdmission) Schedule(ctx context.Context, pin domain.SnapshotPin) error {
	if ctx == nil || admission == nil || admission.repository == nil || len(admission.inventory.Assignments) == 0 {
		return errors.New("verified graph admission required")
	}
	if err := validateIndexPin(pin); err != nil {
		return err
	}
	first := admission.inventory.Assignments[0].Plan
	if pin.CorpusID != first.Meta.CorpusId || pin.SnapshotID != first.Context.SnapshotRef.SnapshotId || pin.Sequence != first.Context.SnapshotRef.Sequence {
		return ErrConflict
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	tx, err := admission.repository.pool.Begin(bounded)
	if err != nil {
		return err
	}
	defer tx.Rollback(bounded)
	var locked string
	if err = tx.QueryRow(bounded, `SELECT publication_id FROM snapshots WHERE publication_id=$1 AND corpus_id=$2 FOR UPDATE`, first.PublicationId, first.Meta.CorpusId).Scan(&locked); err != nil {
		return err
	}
	var revision, floor int64
	if err = tx.QueryRow(bounded, `SELECT registry_revision,registry_history_floor FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, first.Meta.CorpusId).Scan(&revision, &floor); err != nil {
		return err
	}
	if revision != admission.revision || floor != admission.historyFloor {
		return fmt.Errorf("registry changed after graph preflight: %w", ErrConflict)
	}
	if err = verifyGraphPublicationBinding(bounded, tx, first.PublicationId, first.Meta.CorpusId, first.PublicationFence, first.TargetSequence, first.RegistryRevision, first.Context.SnapshotRef); err != nil {
		return err
	}
	requests := make([]*pb.IngestionRequest, len(admission.inventory.Assignments))
	for i, a := range admission.inventory.Assignments {
		var raw []byte
		var digest string
		err = tx.QueryRow(bounded, `SELECT CASE WHEN octet_length(request_payload)<=16777216 THEN request_payload ELSE NULL END,request_hash FROM jobs WHERE job_id=$1 AND corpus_id=$2 AND NOT cancellation_requested FOR SHARE`, a.SourceJobID, first.Meta.CorpusId).Scan(&raw, &digest)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if len(raw) == 0 || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
			return domain.ErrPersistentIntegrity
		}
		request := new(pb.IngestionRequest)
		if err = domain.DecodeWire(raw, request, domain.DefaultWireLimits); err != nil {
			return err
		}
		if request.CorpusId != first.Meta.CorpusId {
			return domain.ErrPersistentIntegrity
		}
		requests[i] = request
		binding := admission.bindings[i]
		if err = verifyGraphAssemblySourceCheckpoint(bounded, tx, first.Meta.CorpusId, a.SourceJobID, binding.SourceCheckpointID, binding.OriginalResolution); err != nil {
			return err
		}
		if err = verifyPublishedGraphSourceBinding(bounded, tx, pin, binding.Source); err != nil {
			return err
		}
		stored, e := loadArtifact(bounded, tx, first.Meta.CorpusId, a.Reference.ArtifactId)
		if e != nil {
			return e
		}
		if !proto.Equal(stored, a.Reference) {
			return domain.ErrPersistentIntegrity
		}
	}
	var existing string
	err = tx.QueryRow(bounded, `SELECT payload_hash FROM graph_job_inventories WHERE publication_id=$1`, first.PublicationId).Scan(&existing)
	if err == nil {
		if existing != admission.digest {
			return ErrConflict
		}
		if err = checkIndexLease(bounded, tx, pin); err != nil {
			return err
		}
		return tx.Commit(bounded)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(bounded, `INSERT INTO graph_job_inventories(publication_id,corpus_id,fence,payload,payload_hash,job_count) VALUES($1,$2,$3,$4,$5,$6)`, first.PublicationId, first.Meta.CorpusId, int64(first.PublicationFence), admission.payload, admission.digest, len(admission.inventory.Assignments))
	if err != nil {
		return err
	}
	for i, a := range admission.inventory.Assignments {
		request := requests[i]
		request.Operation = pb.JobOperation_JOB_OPERATION_REBUILD
		request.IdempotencyKey = a.JobID
		request.ConfigManifest = proto.Clone(a.Plan.ProducerManifest).(*pb.ProducerManifest)
		if err = domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
			return err
		}
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(request)
		if e != nil {
			return e
		}
		request.IdempotencyKey = ""
		semantic, e := proto.MarshalOptions{Deterministic: true}.Marshal(request)
		if e != nil {
			return e
		}
		_, err = tx.Exec(bounded, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload) VALUES($1,$2,$3,$4,$5,$6,$1,$7,$8)`, a.JobID, first.Meta.CorpusId, int16(pb.JobOperation_JOB_OPERATION_REBUILD), int16(pb.JobState_JOB_STATE_QUEUED), int16(pb.JobStage_JOB_STAGE_ASSEMBLE), fmt.Sprintf("%x", sha256.Sum256(semantic)), fmt.Sprintf("%x", sha256.Sum256(raw)), raw)
		if err != nil {
			return err
		}
		_, err = tx.Exec(bounded, `INSERT INTO graph_job_assignments(job_id,publication_id,source_job_id,plan_artifact_id,ordinal) VALUES($1,$2,$3,$4,$5)`, a.JobID, first.PublicationId, a.SourceJobID, a.Reference.ArtifactId, i)
		if err != nil {
			return err
		}
	}
	if err = checkIndexLease(bounded, tx, pin); err != nil {
		return err
	}
	return tx.Commit(bounded)
}

// LoadGraphJobInventory is an authenticated audit locator, not live job authority.
func (r *Repository) LoadGraphJobInventory(ctx context.Context, corpus, publication string) (domain.GraphJobInventory, error) {
	var result domain.GraphJobInventory
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(publication) {
		return result, errors.New("graph inventory identity required")
	}
	var raw []byte
	var digest string
	var fence int64
	var count int
	err := r.pool.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload)<=67108864 THEN payload ELSE NULL END,payload_hash,fence,job_count FROM graph_job_inventories WHERE publication_id=$1 AND corpus_id=$2`, publication, corpus).Scan(&raw, &digest, &fence, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if len(raw) == 0 || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
		return result, domain.ErrPersistentIntegrity
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateGraphJobInventory(result); err != nil {
		return result, domain.ErrPersistentIntegrity
	}
	first := result.Assignments[0].Plan
	if count != len(result.Assignments) || first.Meta.CorpusId != corpus || first.PublicationId != publication || first.PublicationFence != uint64(fence) {
		return result, domain.ErrPersistentIntegrity
	}
	canonical, err := json.Marshal(result)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(canonical)) != digest {
		return result, domain.ErrPersistentIntegrity
	}
	return result, nil
}
