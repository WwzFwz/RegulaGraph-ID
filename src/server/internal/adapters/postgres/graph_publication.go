// Rechecks a graph readiness authority at active-pointer CAS, after snapshot and
// corpus locks. Fingerprints bind the exact source/child checkpoint bytes, fences
// and states observed during source-admitted receipt commit. NOWAIT job locks
// avoid inversion with workers; contention fails retryably without activation.
// Immutable source membership was verified at receipt; the parent remains active
// under the corpus lock. No network/model work occurs in the transaction. Measure
// lock wait/commit p95 and bytes/RSS using configs/benchmark-targets.yaml.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// Caller holds the snapshot/corpus locks. Source and child jobs are distinct by
// inventory validation. Hashes are local persistence fingerprints, not wire IDs.
func graphPublicationJobsHash(ctx context.Context, tx pgx.Tx, publication string, count int) (string, error) {
	if count < 1 || count > 256 {
		return "", domain.ErrPersistentIntegrity
	}
	rows, err := tx.Query(ctx, `SELECT j.job_id FROM jobs j WHERE j.job_id IN (
 SELECT job_id FROM graph_job_assignments WHERE publication_id=$1
 UNION SELECT source_job_id FROM graph_job_assignments WHERE publication_id=$1)
 ORDER BY j.job_id FOR SHARE OF j NOWAIT`, publication)
	if err != nil {
		return "", indexPublicationLockError(err)
	}
	locked := 0
	for rows.Next() {
		locked++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", indexPublicationLockError(err)
	}
	if locked != count*2 {
		return "", domain.ErrPersistentIntegrity
	}
	rows, err = tx.Query(ctx, `SELECT j.job_id,j.corpus_id,j.state,j.stage,j.cancellation_requested,j.lease_fence,
 COALESCE(j.latest_checkpoint_id,''),COALESCE(c.payload_hash,''),COALESCE(c.job_id,''),COALESCE(c.fence,0),COALESCE(c.stage,0),COALESCE(c.terminal_status,0),
 CASE WHEN octet_length(c.payload)<=16777216 AND sum(octet_length(c.payload)) OVER ()<=67108864 THEN c.payload ELSE NULL END
 FROM jobs j LEFT JOIN job_checkpoints c ON c.checkpoint_id=j.latest_checkpoint_id
 WHERE j.job_id IN (SELECT job_id FROM graph_job_assignments WHERE publication_id=$1
 UNION SELECT source_job_id FROM graph_job_assignments WHERE publication_id=$1)
 ORDER BY j.job_id`, publication)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	n := 0
	for rows.Next() {
		var job, corpus, checkpoint, digest, cpJob string
		var state, stage, cpStage, terminal int16
		var fence, cpFence int64
		var cancelled bool
		var raw []byte
		if err = rows.Scan(&job, &corpus, &state, &stage, &cancelled, &fence, &checkpoint, &digest, &cpJob, &cpFence, &cpStage, &terminal, &raw); err != nil {
			return "", err
		}
		if cancelled || len(raw) == 0 || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest || cpJob != job || fence != cpFence || stage != cpStage || terminal != int16(pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) {
			return "", ErrPublicationNotReady
		}
		if stage == int16(pb.JobStage_JOB_STAGE_ASSEMBLE) {
			if state != int16(pb.JobState_JOB_STATE_STAGED) {
				return "", ErrPublicationNotReady
			}
		} else if stage != int16(pb.JobStage_JOB_STAGE_RESOLVE) || state != int16(pb.JobState_JOB_STATE_STAGED) && state != int16(pb.JobState_JOB_STATE_SUCCEEDED) {
			return "", ErrPublicationNotReady
		}
		// JSON array gives unambiguous framing, including all mutable predicates.
		encoded, e := json.Marshal([]any{job, corpus, state, stage, fence, checkpoint, digest, cpJob, cpFence, cpStage, terminal})
		if e != nil {
			return "", e
		}
		_, _ = h.Write(encoded)
		n++
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if n != locked {
		return "", domain.ErrPersistentIntegrity
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func verifyGraphPublication(ctx context.Context, tx pgx.Tx, publication string) error {
	var count int
	err := tx.QueryRow(ctx, `SELECT job_count FROM graph_job_inventories WHERE publication_id=$1`, publication).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var corpus, expectedCatalogHash, actualCatalogHash, expectedJobsHash, operationStatus, operationHash string
	var revision, floor, observedRevision, observedFloor int64
	err = tx.QueryRow(ctx, `SELECT g.corpus_id,g.payload_hash,a.catalog_hash,a.jobs_hash,c.registry_revision,c.registry_history_floor,a.registry_revision,a.history_floor,o.status,o.payload_hash
 FROM graph_generations g JOIN graph_readiness_authority a USING(publication_id)
 JOIN corpus_state c ON c.corpus_id=g.corpus_id
 JOIN publication_operations o ON o.operation_key=$2 AND o.publication_id=g.publication_id
 AND o.backend=$3 AND o.fence=g.fence
 WHERE g.publication_id=$1`, publication, GraphWriteOperation(publication), int16(pb.BackendKind_BACKEND_KIND_NEO4J)).Scan(
		&corpus, &actualCatalogHash, &expectedCatalogHash, &expectedJobsHash, &revision, &floor, &observedRevision, &observedFloor, &operationStatus, &operationHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPublicationNotReady
	}
	if err != nil {
		return err
	}
	if operationStatus != "applied" || actualCatalogHash != expectedCatalogHash || revision != observedRevision || floor != observedFloor {
		return ErrPublicationNotReady
	}
	b, err := loadGraphCatalog(ctx, tx, corpus, publication)
	if err != nil {
		return err
	}
	if operationHash != b.OperationsHash {
		return domain.ErrPersistentIntegrity
	}
	if err = verifyGraphPublicationBinding(ctx, tx, publication, corpus, b.Binding.Fence, b.Binding.Sequence, b.Binding.RegistryRevision, b.Binding.BaseSnapshot); err != nil {
		return err
	}
	actualJobsHash, err := graphPublicationJobsHash(ctx, tx, publication, count)
	if err != nil {
		return err
	}
	if actualJobsHash != expectedJobsHash {
		return ErrPublicationNotReady
	}
	return nil
}
