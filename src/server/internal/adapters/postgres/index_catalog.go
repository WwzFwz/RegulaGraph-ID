// Registers immutable index generations and allocates collision-checked point
// IDs under a publication fence. Snapshot row locks serialize admission against
// commit/abort; every retry must preserve the complete generation, route and
// record payload. This catalog does not itself authenticate source artifacts or
// write Qdrant. The coordinator performs source/registry admission before calling.
// Batch point work is bounded to 256 records/4 MiB; measure transaction lock wait,
// throughput, RSS and p95 under configs/benchmark-targets.yaml (UNMEASURED).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) RegisterIndexGeneration(ctx context.Context, b domain.IndexCatalogBinding) error {
	if err := domain.ValidateIndexCatalogBinding(b); err != nil {
		return err
	}
	payload, err := proto.Marshal(b.Generation)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockIndexPublication(ctx, tx, b); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO index_generations(corpus_id,generation_id,publication_id,fence,endpoint,collection_name,generation_payload)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, b.Generation.Meta.CorpusId, b.Generation.Meta.RecordId, b.PublicationID, int64(b.Fence), b.Endpoint, b.Collection, payload)
	if err != nil {
		return err
	}
	stored, err := loadIndexBinding(ctx, tx, b.Generation.Meta.CorpusId, b.Generation.Meta.RecordId)
	if err != nil {
		return fmt.Errorf("generation identity/physical namespace collision: %w", ErrConflict)
	}
	if !equalIndexBinding(b, stored) {
		return fmt.Errorf("generation binding changed: %w", ErrConflict)
	}
	return tx.Commit(ctx)
}

type indexQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadIndexBinding(ctx context.Context, q indexQuerier, corpus, generation string) (domain.IndexCatalogBinding, error) {
	var b domain.IndexCatalogBinding
	var raw []byte
	var fence int64
	err := q.QueryRow(ctx, `SELECT publication_id,fence,endpoint,collection_name,generation_payload FROM index_generations WHERE corpus_id=$1 AND generation_id=$2`, corpus, generation).
		Scan(&b.PublicationID, &fence, &b.Endpoint, &b.Collection, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	b.Fence = uint64(fence)
	b.Generation = new(pb.IndexGeneration)
	if err = domain.DecodeWire(raw, b.Generation, domain.DefaultWireLimits); err != nil {
		return b, domain.ErrPersistentIntegrity
	}
	if b.Generation.Meta.CorpusId != corpus || b.Generation.Meta.RecordId != generation || domain.ValidateIndexCatalogBinding(b) != nil {
		return b, domain.ErrPersistentIntegrity
	}
	return b, nil
}
func equalIndexBinding(a, b domain.IndexCatalogBinding) bool {
	return a.PublicationID == b.PublicationID && a.Fence == b.Fence && a.Endpoint == b.Endpoint && a.Collection == b.Collection && proto.Equal(a.Generation, b.Generation)
}
func (r *Repository) LoadIndexGeneration(ctx context.Context, corpus, generation string) (domain.IndexCatalogBinding, error) {
	return loadIndexBinding(ctx, r.pool, corpus, generation)
}

func lockIndexPublication(ctx context.Context, tx pgx.Tx, b domain.IndexCatalogBinding) (uint64, error) {
	var corpus string
	var sequence, fence, current int64
	var state int16
	err := tx.QueryRow(ctx, `SELECT s.corpus_id,s.sequence,s.fence,c.publisher_fence,s.state FROM snapshots s
 JOIN corpus_state c ON c.corpus_id=s.corpus_id WHERE s.publication_id=$1 FOR UPDATE OF s,c`, b.PublicationID).
		Scan(&corpus, &sequence, &fence, &current, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if corpus != b.Generation.Meta.CorpusId || uint64(fence) != b.Fence || fence != current ||
		(state != int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING)) {
		return 0, fmt.Errorf("index publication is stale or terminal: %w", ErrConflict)
	}
	return uint64(sequence), nil
}

// ReserveIndexPoints atomically admits the whole selection or nothing. Stored
// payload bytes are an immutable local record, not a cross-language semantic hash.
// Comparisons use protobuf equality. Physical mutation happens only afterward.
func (r *Repository) ReserveIndexPoints(ctx context.Context, b domain.IndexCatalogBinding, records []*pb.IndexRecord) ([]domain.IndexPointAssignment, error) {
	if err := domain.ValidateIndexCatalogBinding(b); err != nil {
		return nil, err
	}
	if len(records) == 0 || len(records) > 256 {
		return nil, errors.New("index point selection outside budget")
	}
	total := 0
	seen := map[string]bool{}
	payloads := make([][]byte, len(records))
	assignments := make([]domain.IndexPointAssignment, len(records))
	for i, record := range records {
		if err := domain.ValidatePairedIndexFilters(record); err != nil {
			return nil, err
		}
		if record.Meta.CorpusId != b.Generation.Meta.CorpusId || record.GenerationId != b.Generation.Meta.RecordId || seen[record.Meta.RecordId] ||
			record.DenseVector == nil || record.SparseVector == nil || record.DenseVector.ModelId != b.Generation.DenseManifest.ModelId || record.DenseVector.Dimensions != b.Generation.DenseManifest.GetDimensions() {
			return nil, errors.New("index point scope/model or identity mismatch")
		}
		if len(record.DenseVector.Values) != int(record.DenseVector.Dimensions) || record.Meta.Visibility.FromSeq > 1<<53-1 ||
			len(record.SparseVector.Indices) == 0 || len(record.SparseVector.Indices) != len(record.SparseVector.Values) {
			return nil, errors.New("index point vector shape or exact visibility range invalid")
		}
		norm := 0.0
		for _, v := range record.DenseVector.Values {
			norm += float64(v) * float64(v)
		}
		if math.Abs(norm-1) > 0.01 || math.IsNaN(norm) {
			return nil, errors.New("index catalog requires normalized dense vectors")
		}
		for j, id := range record.SparseVector.Indices {
			if id == 0 || j > 0 && id <= record.SparseVector.Indices[j-1] || record.SparseVector.Values[j] <= 0 {
				return nil, errors.New("index catalog requires positive ordered sparse terms")
			}
		}
		seen[record.Meta.RecordId] = true
		total += proto.Size(record)
		if total > 4<<20 {
			return nil, errors.New("index point byte budget exceeded")
		}
		var err error
		assignments[i], err = domain.IndexPointIdentity(record.Meta.CorpusId, record.GenerationId, record.Meta.RecordId)
		if err != nil {
			return nil, err
		}
		payloads[i], err = proto.Marshal(record)
		if err != nil {
			return nil, err
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	sequence, err := lockIndexPublication(ctx, tx, b)
	if err != nil {
		return nil, err
	}
	stored, err := loadIndexBinding(ctx, tx, b.Generation.Meta.CorpusId, b.Generation.Meta.RecordId)
	if err != nil {
		return nil, err
	}
	if !equalIndexBinding(b, stored) {
		return nil, fmt.Errorf("index route changed: %w", ErrConflict)
	}
	batch := &pgx.Batch{}
	for i, record := range records {
		if record.Meta.Visibility.FromSeq != sequence || record.Meta.Visibility.ToSeq != nil {
			return nil, errors.New("new index point must start at reserved sequence")
		}
		a := assignments[i]
		batch.Queue(`INSERT INTO index_points(corpus_id,generation_id,record_id,point_id,identity_digest,record_payload)
 VALUES($1,$2,$3,$4::uuid,$5,$6) ON CONFLICT(corpus_id,generation_id,record_id) DO NOTHING`,
			record.Meta.CorpusId, record.GenerationId, record.Meta.RecordId, a.PointID, a.IdentityDigest, payloads[i])
	}
	results := tx.SendBatch(ctx, batch)
	err = results.Close()
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			return nil, fmt.Errorf("point UUID collision: %w", ErrConflict)
		}
		return nil, err
	}
	// One read for the whole selection; no query per record.
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Meta.RecordId
	}
	rows, err := tx.Query(ctx, `SELECT record_id,point_id::text,identity_digest,record_payload FROM index_points
 WHERE corpus_id=$1 AND generation_id=$2 AND record_id=ANY($3::text[])`, b.Generation.Meta.CorpusId, b.Generation.Meta.RecordId, ids)
	if err != nil {
		return nil, err
	}
	expected := make(map[string]int, len(records))
	for i, id := range ids {
		expected[id] = i
	}
	count := 0
	for rows.Next() {
		var id, point, digest string
		var raw []byte
		if err = rows.Scan(&id, &point, &digest, &raw); err != nil {
			break
		}
		i, ok := expected[id]
		if !ok {
			err = domain.ErrPersistentIntegrity
			break
		}
		stored := new(pb.IndexRecord)
		if err = domain.DecodeWire(raw, stored, domain.DefaultWireLimits); err != nil {
			break
		}
		if point != assignments[i].PointID || digest != assignments[i].IdentityDigest || !proto.Equal(records[i], stored) {
			err = fmt.Errorf("immutable index record conflict: %w", ErrConflict)
			break
		}
		count++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if count != len(records) {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return assignments, nil
}
