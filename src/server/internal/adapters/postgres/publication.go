// Menyimpan state machine publication S01 di sekitar snapshot immutable.
// Peran: menjadi source of truth untuk reservation, expected backend generation, receipt,
// active pointer, publication outbox, abort, dan pin pembaca historis.
// Kontrak: satu publisher terbuka per corpus; sequence/fence monotonik; receipt harus cocok
// exact dengan generation/checksum/count/fence dan search-ready; commit membaca bukti yang
// tersimpan lalu CAS parent ke target. Snapshot lama tetap dapat dirujuk dan read lease
// melindungi retention. Transaksi ini tidak mencakup mutation Neo4j/Qdrant.
// Benchmark: ukur commit/receipt latency p50/p95/p99, contention publisher, recovery time,
// read-pin overhead, dan error rate dengan failure injection.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: reserve/stage/receipt/commit/replay/abort/read-pin S01 aktif; backend compensation
// dan garbage collection menyeluruh dilanjutkan U01/O01.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) ReservePublication(ctx context.Context, publicationID, jobID, corpusID, snapshotID, expectedParentID string) (PublicationReservation, error) {
	if !storageIDPattern.MatchString(publicationID) || !storageIDPattern.MatchString(corpusID) ||
		!storageIDPattern.MatchString(snapshotID) || jobID != "" && !storageIDPattern.MatchString(jobID) {
		return PublicationReservation{}, errors.New("invalid publication reservation identifiers")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PublicationReservation{}, fmt.Errorf("begin publication reservation: %w", err)
	}
	defer tx.Rollback(ctx)
	var existing PublicationReservation
	var existingJob, existingParent sql.NullString
	var seq, fence int64
	var state int16
	err = tx.QueryRow(ctx, `SELECT publication_id,job_id,corpus_id,snapshot_id,parent_snapshot_id,sequence,fence,state
      FROM snapshots WHERE publication_id=$1 FOR UPDATE`, publicationID).Scan(&existing.PublicationID, &existingJob,
		&existing.CorpusID, &existing.SnapshotID, &existingParent, &seq, &fence, &state)
	if err == nil {
		existing.JobID, existing.ParentSnapshotID = existingJob.String, existingParent.String
		existing.Sequence, existing.Fence, existing.State = uint64(seq), uint64(fence), pb.SnapshotState(state)
		if existing.JobID != jobID || existing.CorpusID != corpusID || existing.SnapshotID != snapshotID || existing.ParentSnapshotID != expectedParentID {
			return PublicationReservation{}, fmt.Errorf("publication id reused with different reservation: %w", ErrConflict)
		}
		if err = tx.Commit(ctx); err != nil {
			return PublicationReservation{}, err
		}
		return existing, nil
	}
	if err != pgx.ErrNoRows {
		return PublicationReservation{}, fmt.Errorf("inspect publication reservation: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO corpus_state(corpus_id) VALUES ($1)
      ON CONFLICT (corpus_id) DO NOTHING`, corpusID); err != nil {
		return PublicationReservation{}, fmt.Errorf("ensure publication corpus: %w", err)
	}
	if jobID != "" {
		var jobCorpus string
		var jobState int16
		var cancellationRequested bool
		if err = tx.QueryRow(ctx, `SELECT corpus_id,state,cancellation_requested FROM jobs WHERE job_id=$1 FOR SHARE`, jobID).Scan(&jobCorpus, &jobState, &cancellationRequested); err == pgx.ErrNoRows {
			return PublicationReservation{}, ErrNotFound
		} else if err != nil {
			return PublicationReservation{}, fmt.Errorf("load publication job: %w", err)
		} else if jobCorpus != corpusID {
			return PublicationReservation{}, fmt.Errorf("publication job belongs to another corpus: %w", ErrConflict)
		} else if cancellationRequested || jobState != int16(pb.JobState_JOB_STATE_STAGED) &&
			jobState != int16(pb.JobState_JOB_STATE_VALIDATING) && jobState != int16(pb.JobState_JOB_STATE_PUBLISHING) {
			return PublicationReservation{}, fmt.Errorf("publication job is not in a publishable state: %w", ErrConflict)
		}
	}
	var active sql.NullString
	err = tx.QueryRow(ctx, `SELECT active_snapshot_id,next_snapshot_sequence,publisher_fence
      FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, corpusID).Scan(&active, &seq, &fence)
	if err != nil {
		return PublicationReservation{}, fmt.Errorf("lock corpus publisher: %w", err)
	}
	if active.String != expectedParentID {
		return PublicationReservation{}, ErrSnapshotCASConflict
	}
	fence++
	if _, err = tx.Exec(ctx, `UPDATE corpus_state SET next_snapshot_sequence=$2,publisher_fence=$3,
      updated_at=clock_timestamp() WHERE corpus_id=$1`, corpusID, seq+1, fence); err != nil {
		return PublicationReservation{}, fmt.Errorf("advance publisher epoch: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO snapshots(snapshot_id,corpus_id,sequence,parent_snapshot_id,
      publication_id,job_id,fence,state) VALUES ($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7,$8)`,
		snapshotID, corpusID, seq, expectedParentID, publicationID, jobID, fence,
		int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING))
	if err != nil {
		return PublicationReservation{}, fmt.Errorf("reserve snapshot: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return PublicationReservation{}, fmt.Errorf("commit publication reservation: %w", err)
	}
	return PublicationReservation{PublicationID: publicationID, JobID: jobID, CorpusID: corpusID,
		SnapshotID: snapshotID, ParentSnapshotID: expectedParentID, Sequence: uint64(seq),
		Fence: uint64(fence), State: pb.SnapshotState_SNAPSHOT_STATE_STAGING}, nil
}

func (r *Repository) StagePublication(ctx context.Context, manifest *pb.PublicationManifest) error {
	if manifest == nil || len(manifest.GetAcknowledgements()) != 0 {
		return errors.New("staged publication requires a manifest without acknowledgements")
	}
	if err := domain.ValidateWire(manifest, domain.DefaultWireLimits); err != nil {
		return fmt.Errorf("validate staged publication: %w", err)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal publication manifest: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin stage publication: %w", err)
	}
	defer tx.Rollback(ctx)
	var corpusID, snapshotID string
	var parent sql.NullString
	var sequence, fence int64
	var state int16
	var existing []byte
	err = tx.QueryRow(ctx, `SELECT corpus_id,snapshot_id,parent_snapshot_id,sequence,fence,state,manifest_payload
      FROM snapshots WHERE publication_id=$1 FOR UPDATE`, manifest.Meta.RecordId).Scan(
		&corpusID, &snapshotID, &parent, &sequence, &fence, &state, &existing)
	if err == pgx.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load publication reservation: %w", err)
	}
	if state != int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) {
		return fmt.Errorf("cannot stage terminal or publishing snapshot: %w", ErrConflict)
	}
	parentID := ""
	if manifest.ParentRef != nil {
		parentID = manifest.ParentRef.SnapshotId
	}
	if corpusID != manifest.Meta.CorpusId || snapshotID != manifest.SnapshotRef.SnapshotId ||
		uint64(sequence) != manifest.SnapshotRef.Sequence || uint64(fence) != manifest.Fence || parent.String != parentID {
		return fmt.Errorf("manifest differs from reserved slot: %w", ErrConflict)
	}
	if manifest.ParentRef != nil {
		var parentCorpus, parentManifestHash, parentGeneration string
		var parentSequence int64
		var parentState int16
		if err = tx.QueryRow(ctx, `SELECT corpus_id,sequence,manifest_hash,representation_generation,state
			FROM snapshots WHERE snapshot_id=$1`, parentID).Scan(&parentCorpus, &parentSequence,
			&parentManifestHash, &parentGeneration, &parentState); err != nil {
			return fmt.Errorf("load authoritative parent snapshot: %w", err)
		}
		if parentCorpus != manifest.ParentRef.CorpusId || uint64(parentSequence) != manifest.ParentRef.Sequence ||
			parentManifestHash != manifest.ParentRef.ManifestHash.Sha256 || parentGeneration != manifest.ParentRef.RepresentationGeneration ||
			parentState != int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED) {
			return fmt.Errorf("publication parent reference is not authoritative: %w", ErrConflict)
		}
	}
	if existing != nil {
		stored, decodeErr := unmarshalManifest(existing)
		if decodeErr != nil {
			return decodeErr
		}
		if !proto.Equal(manifest, stored) {
			return fmt.Errorf("publication already staged with different manifest: %w", ErrConflict)
		}
		return tx.Commit(ctx)
	}
	for _, generation := range manifest.BackendGenerations {
		if generation.ExpectedCounts.Expected > math.MaxInt64 {
			return errors.New("backend expected count exceeds PostgreSQL bigint")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO publication_backends(publication_id,backend,generation,
          operations_checksum,expected_count) VALUES ($1,$2,$3,$4,$5)`, manifest.Meta.RecordId,
			int16(generation.Backend), generation.Generation, generation.OperationsChecksum.Sha256,
			int64(generation.ExpectedCounts.Expected)); err != nil {
			return fmt.Errorf("stage backend generation: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE snapshots SET state=$2,manifest_payload=$3,manifest_hash=$4,
		representation_generation=$5 WHERE publication_id=$1`, manifest.Meta.RecordId,
		int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING), payload, manifest.SnapshotRef.ManifestHash.Sha256,
		manifest.SnapshotRef.RepresentationGeneration)
	if err != nil {
		return fmt.Errorf("stage publication manifest: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *Repository) RecordBackendReceipt(ctx context.Context, receipt *pb.BackendReceipt) error {
	if receipt == nil {
		return errors.New("backend receipt is required")
	}
	if err := domain.ValidateWire(receipt, domain.DefaultWireLimits); err != nil {
		return fmt.Errorf("validate backend receipt: %w", err)
	}
	if !receipt.DurableAck || !receipt.SearchReady || receipt.Counts.Accepted != receipt.Counts.Expected || receipt.Counts.Rejected != 0 {
		return ErrPublicationNotReady
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("marshal backend receipt: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin backend receipt: %w", err)
	}
	defer tx.Rollback(ctx)
	var generation, checksum string
	var expected, fence int64
	var state int16
	err = tx.QueryRow(ctx, `SELECT b.generation,b.operations_checksum,b.expected_count,s.fence,s.state
      FROM publication_backends b JOIN snapshots s USING(publication_id)
      WHERE b.publication_id=$1 AND b.backend=$2 FOR UPDATE`, receipt.PublicationId, int16(receipt.Backend)).Scan(
		&generation, &checksum, &expected, &fence, &state)
	if err == pgx.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load expected backend receipt: %w", err)
	}
	if state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED) {
		return fmt.Errorf("snapshot does not accept backend receipts: %w", ErrConflict)
	}
	if generation != receipt.Generation || checksum != receipt.OperationsChecksum.Sha256 ||
		uint64(expected) != receipt.Counts.Expected || uint64(fence) != receipt.Fence {
		return ErrStaleFence
	}
	tag, err := tx.Exec(ctx, `INSERT INTO backend_receipts(publication_id,backend,fence,generation,
      operations_checksum,expected_count,accepted_count,rejected_count,durable_ack,search_ready,payload)
      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (publication_id,backend) DO NOTHING`,
		receipt.PublicationId, int16(receipt.Backend), int64(receipt.Fence), receipt.Generation,
		receipt.OperationsChecksum.Sha256, int64(receipt.Counts.Expected), int64(receipt.Counts.Accepted),
		int64(receipt.Counts.Rejected), receipt.DurableAck, receipt.SearchReady, payload)
	if err != nil {
		return fmt.Errorf("insert backend receipt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var stored []byte
		if err = tx.QueryRow(ctx, `SELECT payload FROM backend_receipts WHERE publication_id=$1 AND backend=$2`,
			receipt.PublicationId, int16(receipt.Backend)).Scan(&stored); err != nil {
			return err
		}
		storedReceipt, decodeErr := unmarshalReceipt(stored)
		if decodeErr != nil {
			return decodeErr
		}
		if !proto.Equal(receipt, storedReceipt) {
			return fmt.Errorf("backend receipt changed after acknowledgement: %w", ErrConflict)
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) CommitPublication(ctx context.Context, publicationID string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin publication commit: %w", err)
	}
	defer tx.Rollback(ctx)
	var payload []byte
	var corpusID, snapshotID string
	var parent, jobID sql.NullString
	var sequence, fence int64
	var state int16
	err = tx.QueryRow(ctx, `SELECT manifest_payload,corpus_id,snapshot_id,parent_snapshot_id,job_id,
      sequence,fence,state FROM snapshots WHERE publication_id=$1 FOR UPDATE`, publicationID).Scan(
		&payload, &corpusID, &snapshotID, &parent, &jobID, &sequence, &fence, &state)
	if err == pgx.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load publication for commit: %w", err)
	}
	if payload == nil {
		return ErrPublicationNotReady
	}
	if state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED) {
		return fmt.Errorf("snapshot cannot be committed from its current state: %w", ErrConflict)
	}
	manifest, err := unmarshalManifest(payload)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT payload FROM backend_receipts WHERE publication_id=$1 ORDER BY backend`, publicationID)
	if err != nil {
		return fmt.Errorf("load backend receipts: %w", err)
	}
	for rows.Next() {
		var receiptPayload []byte
		if err = rows.Scan(&receiptPayload); err != nil {
			rows.Close()
			return err
		}
		receipt, decodeErr := unmarshalReceipt(receiptPayload)
		if decodeErr != nil {
			rows.Close()
			return decodeErr
		}
		manifest.Acknowledgements = append(manifest.Acknowledgements, receipt)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	authoritativeParent, err := loadSnapshotRefTx(ctx, tx, parent.String)
	if err != nil {
		return err
	}
	if err = domain.VerifyPublicationReady(manifest, authoritativeParent, uint64(fence)); err != nil {
		return fmt.Errorf("verify publication readiness: %w: %v", ErrPublicationNotReady, err)
	}
	var active sql.NullString
	if err = tx.QueryRow(ctx, `SELECT active_snapshot_id FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, corpusID).Scan(&active); err != nil {
		return fmt.Errorf("lock active snapshot: %w", err)
	}
	if state == int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED) {
		return finalizePublicationTx(ctx, tx, publicationID, snapshotID, corpusID, jobID.String)
	}
	if active.String != parent.String {
		return ErrSnapshotCASConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE snapshots SET state=$2,published_at=clock_timestamp()
      WHERE publication_id=$1`, publicationID, int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED)); err != nil {
		return fmt.Errorf("mark snapshot published: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE corpus_state SET active_snapshot_id=$2,updated_at=clock_timestamp()
      WHERE corpus_id=$1`, corpusID, snapshotID); err != nil {
		return fmt.Errorf("activate snapshot: %w", err)
	}
	return finalizePublicationTx(ctx, tx, publicationID, snapshotID, corpusID, jobID.String)
}

func finalizePublicationTx(ctx context.Context, tx pgx.Tx, publicationID, snapshotID, corpusID, jobID string) error {
	if jobID != "" {
		tag, err := tx.Exec(ctx, `UPDATE jobs SET state=$2,lease_owner=NULL,lease_expires_at=NULL,
			updated_at=clock_timestamp() WHERE job_id=$1 AND corpus_id=$3
			AND cancellation_requested=false AND state IN ($4,$2)`, jobID,
			int16(pb.JobState_JOB_STATE_SUCCEEDED), corpusID, int16(pb.JobState_JOB_STATE_PUBLISHING))
		if err != nil {
			return fmt.Errorf("complete publication job: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("publication job missing during completion: %w", ErrConflict)
		}
	}
	payload, _ := json.Marshal(map[string]string{"publication_id": publicationID, "snapshot_id": snapshotID})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,topic,aggregate_id,payload)
      VALUES ($1,'snapshot.published',$2,$3::jsonb) ON CONFLICT (event_id) DO NOTHING`,
		"snapshot-published:"+publicationID, snapshotID, string(payload)); err != nil {
		return fmt.Errorf("write publication outbox: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *Repository) PinActiveSnapshot(ctx context.Context, corpusID, leaseID, ownerID string, ttl time.Duration) (SnapshotPin, error) {
	if ttl <= 0 || !storageIDPattern.MatchString(leaseID) || !storageIDPattern.MatchString(ownerID) {
		return SnapshotPin{}, errors.New("valid lease, owner and positive ttl required")
	}
	var pin SnapshotPin
	pin.LeaseID, pin.CorpusID, pin.OwnerID = leaseID, corpusID, ownerID
	var sequence int64
	err := r.pool.QueryRow(ctx, `INSERT INTO snapshot_read_leases(lease_id,corpus_id,snapshot_id,owner_id,expires_at)
      SELECT $2,c.corpus_id,c.active_snapshot_id,$3,clock_timestamp()+$4::interval
      FROM corpus_state c JOIN snapshots s ON s.snapshot_id=c.active_snapshot_id
      WHERE c.corpus_id=$1 AND s.state=$5
      ON CONFLICT (lease_id) DO UPDATE SET expires_at=EXCLUDED.expires_at
        WHERE snapshot_read_leases.corpus_id=EXCLUDED.corpus_id
          AND snapshot_read_leases.snapshot_id=EXCLUDED.snapshot_id
          AND snapshot_read_leases.owner_id=EXCLUDED.owner_id
      RETURNING snapshot_id,expires_at,(SELECT sequence FROM snapshots WHERE snapshot_id=snapshot_read_leases.snapshot_id)`,
		corpusID, leaseID, ownerID, ttl.String(), int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED)).Scan(
		&pin.SnapshotID, &pin.ExpiresAt, &sequence)
	if err == pgx.ErrNoRows {
		return SnapshotPin{}, ErrNotFound
	}
	if err != nil {
		return SnapshotPin{}, fmt.Errorf("pin active snapshot: %w", err)
	}
	pin.Sequence = uint64(sequence)
	return pin, nil
}

func (r *Repository) ReleaseSnapshotPin(ctx context.Context, leaseID, ownerID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM snapshot_read_leases WHERE lease_id=$1 AND owner_id=$2`, leaseID, ownerID)
	if err != nil {
		return fmt.Errorf("release snapshot pin: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func unmarshalManifest(payload []byte) (*pb.PublicationManifest, error) {
	message := &pb.PublicationManifest{}
	if err := proto.Unmarshal(payload, message); err != nil {
		return nil, fmt.Errorf("database contains corrupt publication manifest: %w", err)
	}
	return message, nil
}

func unmarshalReceipt(payload []byte) (*pb.BackendReceipt, error) {
	message := &pb.BackendReceipt{}
	if err := proto.Unmarshal(payload, message); err != nil {
		return nil, fmt.Errorf("database contains corrupt backend receipt: %w", err)
	}
	return message, nil
}

func (r *Repository) LoadPublicationManifest(ctx context.Context, publicationID string) (*pb.PublicationManifest, error) {
	var payload []byte
	err := r.pool.QueryRow(ctx, `SELECT manifest_payload FROM snapshots WHERE publication_id=$1`, publicationID).Scan(&payload)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load publication manifest: %w", err)
	}
	if payload == nil {
		return nil, ErrPublicationNotReady
	}
	manifest, err := unmarshalManifest(payload)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT payload FROM backend_receipts WHERE publication_id=$1 ORDER BY backend`, publicationID)
	if err != nil {
		return nil, fmt.Errorf("load publication receipts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var receiptPayload []byte
		if err = rows.Scan(&receiptPayload); err != nil {
			return nil, err
		}
		receipt, decodeErr := unmarshalReceipt(receiptPayload)
		if decodeErr != nil {
			return nil, decodeErr
		}
		manifest.Acknowledgements = append(manifest.Acknowledgements, receipt)
	}
	return manifest, rows.Err()
}

func (r *Repository) LoadSnapshotRef(ctx context.Context, snapshotID string) (*pb.SnapshotRef, error) {
	return loadSnapshotRefQuerier(ctx, r.pool, snapshotID)
}

type snapshotRefQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadSnapshotRefTx(ctx context.Context, tx pgx.Tx, snapshotID string) (*pb.SnapshotRef, error) {
	if snapshotID == "" {
		return nil, nil
	}
	return loadSnapshotRefQuerier(ctx, tx, snapshotID)
}

func loadSnapshotRefQuerier(ctx context.Context, query snapshotRefQuerier, snapshotID string) (*pb.SnapshotRef, error) {
	var corpusID, manifestHash, generation string
	var sequence int64
	var state int16
	err := query.QueryRow(ctx, `SELECT corpus_id,sequence,manifest_hash,representation_generation,state
		FROM snapshots WHERE snapshot_id=$1`, snapshotID).Scan(&corpusID, &sequence, &manifestHash, &generation, &state)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load snapshot reference: %w", err)
	}
	if state != int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED) || manifestHash == "" || generation == "" {
		return nil, ErrPublicationNotReady
	}
	return &pb.SnapshotRef{CorpusId: corpusID, SnapshotId: snapshotID, Sequence: uint64(sequence),
		ManifestHash: &pb.ContentHash{Sha256: manifestHash}, RepresentationGeneration: generation}, nil
}

func (r *Repository) ActiveSnapshot(ctx context.Context, corpusID string) (string, uint64, error) {
	var id string
	var sequence int64
	err := r.pool.QueryRow(ctx, `SELECT s.snapshot_id,s.sequence FROM corpus_state c
      JOIN snapshots s ON s.snapshot_id=c.active_snapshot_id WHERE c.corpus_id=$1`, corpusID).Scan(&id, &sequence)
	if err == pgx.ErrNoRows {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, fmt.Errorf("read active snapshot: %w", err)
	}
	return id, uint64(sequence), nil
}

func (r *Repository) AbortPublication(ctx context.Context, publicationID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state int16
	if err = tx.QueryRow(ctx, `SELECT state FROM snapshots WHERE publication_id=$1 FOR UPDATE`, publicationID).Scan(&state); err == pgx.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock publication for abort: %w", err)
	}
	if state != int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_ABORTING) {
		return fmt.Errorf("publication cannot be aborted from its current state: %w", ErrConflict)
	}
	var uncompensated bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publication_operations
		WHERE publication_id=$1 AND status IN ('planned','applied'))`, publicationID).Scan(&uncompensated)
	if err != nil {
		return fmt.Errorf("inspect publication compensation: %w", err)
	}
	if uncompensated {
		return fmt.Errorf("publication has uncompensated backend operations: %w", ErrConflict)
	}
	tag, err := tx.Exec(ctx, `UPDATE snapshots SET state=$2 WHERE publication_id=$1
      AND state IN ($3,$4,$5)`, publicationID, int16(pb.SnapshotState_SNAPSHOT_STATE_ABORTED),
		int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING), int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING),
		int16(pb.SnapshotState_SNAPSHOT_STATE_ABORTING))
	if err != nil {
		return fmt.Errorf("abort publication: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (r *Repository) RecordPublicationOperation(ctx context.Context, publicationID string, backend pb.BackendKind,
	operationKey, payloadHash, status string, fence uint64) error {
	if !sha256Pattern.MatchString(payloadHash) || fence == 0 || status != "planned" && status != "applied" && status != "compensated" {
		return errors.New("invalid publication operation")
	}
	tag, err := r.pool.Exec(ctx, `INSERT INTO publication_operations(operation_key,publication_id,backend,payload_hash,fence,status)
		SELECT $1,s.publication_id,$3,$4,$5,$6 FROM snapshots s
		WHERE s.publication_id=$2 AND s.fence=$5 AND s.state IN ($7,$8,$9)
		FOR UPDATE OF s
      ON CONFLICT (operation_key) DO UPDATE SET status=EXCLUDED.status,updated_at=clock_timestamp()
      WHERE publication_operations.publication_id=EXCLUDED.publication_id
        AND publication_operations.backend=EXCLUDED.backend
        AND publication_operations.payload_hash=EXCLUDED.payload_hash
		AND publication_operations.fence=EXCLUDED.fence
		AND (publication_operations.status=EXCLUDED.status
		  OR publication_operations.status='planned' AND EXCLUDED.status IN ('applied','compensated')
		  OR publication_operations.status='applied' AND EXCLUDED.status='compensated')`,
		operationKey, publicationID, int16(backend), payloadHash, int64(fence), status,
		int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING), int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING),
		int16(pb.SnapshotState_SNAPSHOT_STATE_ABORTING))
	if err != nil {
		return fmt.Errorf("record publication operation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("publication operation conflict or invalid state regression: %w", ErrConflict)
	}
	return nil
}
