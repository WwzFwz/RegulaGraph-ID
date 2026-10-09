// Reads only published, receipted index generations under a live snapshot pin.
// Batched record lookup checks full identity/digest, payload scope and visibility;
// SQL caps aggregate returned payload bytes before transfer. Missing or corrupt
// records fail the whole selection. No current-active-pointer lookup substitutes
// another generation midway through a request. Caller owns authorization and
// releases the lease. Profile pool/lease/read p95,p99 and RSS under benchmark-
// targets.yaml; these invariant checks do not prove relevance or legal accuracy.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func validateIndexPin(pin domain.SnapshotPin) error {
	for _, id := range []string{pin.LeaseID, pin.OwnerID, pin.CorpusID, pin.SnapshotID} {
		if !storageIDPattern.MatchString(id) {
			return errors.New("complete trusted snapshot pin required")
		}
	}
	if pin.Sequence == 0 || pin.Sequence > 1<<53-1 || pin.ExpiresAt.IsZero() {
		return errors.New("invalid snapshot pin sequence or expiry")
	}
	return nil
}

// LoadPinnedIndex admits an owned generation view; a returned object is not a
// perpetual capability. Later reads still verify the live lease in PostgreSQL.
func (r *Repository) LoadPinnedIndex(ctx context.Context, pin domain.SnapshotPin) (*domain.PinnedIndex, error) {
	if err := validateIndexPin(pin); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	tx, err := r.pool.BeginTx(bounded, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(bounded)
	result, err := readPinnedIndex(bounded, tx, pin)
	if err != nil {
		return nil, err
	}
	if err = checkIndexLease(bounded, tx, pin); err != nil {
		return nil, err
	}
	if err = tx.Commit(bounded); err != nil {
		return nil, err
	}
	return result, nil
}

func checkIndexLease(ctx context.Context, tx pgx.Tx, pin domain.SnapshotPin) error {
	var alive bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM snapshot_read_leases WHERE lease_id=$1 AND owner_id=$2
 AND corpus_id=$3 AND snapshot_id=$4 AND expires_at=$5 AND expires_at>clock_timestamp())`, pin.LeaseID, pin.OwnerID, pin.CorpusID, pin.SnapshotID, pin.ExpiresAt).Scan(&alive)
	if err != nil {
		return err
	}
	if !alive {
		return domain.ErrLeaseUnavailable
	}
	return nil
}

func readPinnedIndex(ctx context.Context, tx pgx.Tx, pin domain.SnapshotPin) (*domain.PinnedIndex, error) {
	if err := checkIndexLease(ctx, tx, pin); err != nil {
		return nil, err
	}
	var publication string
	var raw []byte
	var sequence, fence int64
	err := tx.QueryRow(ctx, `SELECT publication_id,sequence,fence,
 CASE WHEN octet_length(manifest_payload)<=$4 THEN manifest_payload ELSE NULL END
 FROM snapshots WHERE snapshot_id=$1 AND corpus_id=$2 AND state=$3`, pin.SnapshotID, pin.CorpusID, int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED), domain.DefaultWireLimits.MaxBytes).Scan(&publication, &sequence, &fence, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	manifest := new(pb.PublicationManifest)
	if len(raw) == 0 || uint64(sequence) != pin.Sequence {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = domain.DecodeWire(raw, manifest, domain.DefaultWireLimits); err != nil {
		return nil, domain.ErrPersistentIntegrity
	}
	if manifest.Meta.RecordId != publication || manifest.Meta.CorpusId != pin.CorpusID || manifest.SnapshotRef.SnapshotId != pin.SnapshotID || manifest.SnapshotRef.CorpusId != pin.CorpusID || manifest.SnapshotRef.Sequence != pin.Sequence || manifest.Fence != uint64(fence) || len(manifest.Acknowledgements) != 0 {
		return nil, domain.ErrPersistentIntegrity
	}
	rows, err := tx.Query(ctx, `SELECT CASE WHEN octet_length(payload)<=65536 THEN payload ELSE NULL END FROM backend_receipts WHERE publication_id=$1 ORDER BY backend`, publication)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			break
		}
		receipt := new(pb.BackendReceipt)
		if len(data) == 0 {
			err = domain.ErrPersistentIntegrity
			break
		}
		if err = domain.DecodeWire(data, receipt, domain.DefaultWireLimits); err != nil {
			break
		}
		manifest.Acknowledgements = append(manifest.Acknowledgements, receipt)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	parent, err := loadSnapshotRefTx(ctx, tx, manifest.GetParentRef().GetSnapshotId())
	if err != nil {
		return nil, err
	}
	if err = domain.VerifyPublicationReady(manifest, parent, uint64(fence)); err != nil {
		return nil, fmt.Errorf("pinned publication integrity: %w", err)
	}
	binding, err := loadIndexBinding(ctx, tx, pin.CorpusID, manifest.SnapshotRef.RepresentationGeneration)
	if err != nil {
		return nil, err
	}
	sourceSnapshot := manifest.SnapshotRef
	if binding.PublicationID != publication {
		reuse, e := loadIndexReuse(ctx, tx, publication)
		if e != nil {
			return nil, e
		}
		if !equalIndexBinding(binding, reuse.Index) {
			return nil, domain.ErrPersistentIntegrity
		}
		if e = validateIndexReuseManifest(reuse, manifest); e != nil {
			return nil, e
		}
		if e = verifyIndexReuseOrigin(ctx, tx, reuse); e != nil {
			return nil, e
		}
		sourceSnapshot = reuse.Source
	} else if binding.Fence != uint64(fence) {
		return nil, domain.ErrPersistentIntegrity
	}
	matched := false
	for _, backend := range manifest.BackendGenerations {
		if backend.Backend == pb.BackendKind_BACKEND_KIND_QDRANT && backend.Generation == binding.Generation.Meta.RecordId {
			matched = true
		}
	}
	if !matched {
		return nil, errors.New("published snapshot has no matching Qdrant generation receipt")
	}
	return &domain.PinnedIndex{Pin: pin, Snapshot: proto.Clone(manifest.SnapshotRef).(*pb.SnapshotRef), Binding: binding, SourceSnapshot: proto.Clone(sourceSnapshot).(*pb.SnapshotRef)}, nil
}

func (r *Repository) LoadPinnedIndexRecords(ctx context.Context, pin domain.SnapshotPin, ids []string, maximumBytes uint64) ([]domain.IndexCatalogRecord, error) {
	if err := validateIndexPin(pin); err != nil {
		return nil, err
	}
	if len(ids) > 256 || maximumBytes == 0 || maximumBytes > 16<<20 {
		return nil, errors.New("bounded index selection and byte budget required")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !storageIDPattern.MatchString(id) || seen[id] {
			return nil, errors.New("invalid or duplicate index selection")
		}
		seen[id] = true
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	tx, err := r.pool.BeginTx(bounded, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(bounded)
	index, err := readPinnedIndex(bounded, tx, pin)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(bounded, `SELECT record_id,point_id::text,identity_digest,
 CASE WHEN sum(octet_length(record_payload)) OVER ()<=$4 THEN record_payload ELSE NULL END
 FROM index_points WHERE corpus_id=$1 AND generation_id=$2 AND record_id=ANY($3::text[])`, pin.CorpusID, index.Binding.Generation.Meta.RecordId, ids, int64(maximumBytes))
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.IndexCatalogRecord{}
	for rows.Next() {
		var id, point, digest string
		var raw []byte
		if err = rows.Scan(&id, &point, &digest, &raw); err != nil {
			break
		}
		if len(raw) == 0 {
			err = ErrResultLimit
			break
		}
		record := new(pb.IndexRecord)
		if err = domain.DecodeWire(raw, record, domain.DefaultWireLimits); err != nil {
			break
		}
		identity, e := domain.IndexPointIdentity(pin.CorpusID, index.Binding.Generation.Meta.RecordId, id)
		if e != nil || identity.PointID != point || identity.IdentityDigest != digest || record.Meta.RecordId != id || record.Meta.CorpusId != pin.CorpusID || record.GenerationId != index.Binding.Generation.Meta.RecordId {
			err = domain.ErrPersistentIntegrity
			break
		}
		if err = domain.ValidatePairedIndexFilters(record); err != nil {
			break
		}
		if record.Meta.Visibility.FromSeq > pin.Sequence || record.Meta.Visibility.ToSeq != nil && *record.Meta.Visibility.ToSeq <= pin.Sequence {
			err = domain.ErrPersistentIntegrity
			break
		}
		byID[id] = domain.IndexCatalogRecord{PointID: point, Record: record}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(byID) != len(ids) {
		return nil, ErrNotFound
	}
	if err = checkIndexLease(bounded, tx, pin); err != nil {
		return nil, err
	}
	if err = tx.Commit(bounded); err != nil {
		return nil, err
	}
	out := make([]domain.IndexCatalogRecord, len(ids))
	for i, id := range ids {
		out[i] = byID[id]
	}
	return out, nil
}
