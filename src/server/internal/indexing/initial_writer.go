// Dispatches an admitted first-snapshot index through immutable PostgreSQL
// catalog/intent, bounded Qdrant upserts, full exact readback and serving probes.
// Failed writes retain their intent for identical retry; they do not publish,
// delete or mark compensation complete. A receipt covers only Qdrant. The normal
// publication coordinator must separately require every staged backend receipt.
// Only a single-shard/single-replica route is supported; no incremental closures.
// Measure per-batch/lock/readback latency and RSS using benchmark-targets.yaml;
// fixture success is not required performance or retrieval-quality acceptance.
package indexing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
)

type InitialIndexStore interface {
	AcquireIndexWriteLock(context.Context, string) (func(), error)
	RegisterIndexGeneration(context.Context, domain.IndexCatalogBinding) error
	ReserveIndexPoints(context.Context, domain.IndexCatalogBinding, []*pb.IndexRecord) ([]domain.IndexPointAssignment, error)
	EnsureIndexWriteIntent(context.Context, domain.IndexCatalogBinding, string, string, uint64) error
	RecordPublicationOperation(context.Context, string, pb.BackendKind, string, string, string, uint64) error
	LoadPublicationManifest(context.Context, string) (*pb.PublicationManifest, error)
	RecordBackendReceipt(context.Context, *pb.BackendReceipt) error
}
type InitialIndexBackend interface {
	Endpoint() string
	Binding() qdrant.Binding
	EnsureCollection(context.Context) error
	Upsert(context.Context, []qdrant.Point) error
	VerifyPoints(context.Context, []qdrant.Point) error
	VerifyInitialServing(context.Context, uint64, uint64, qdrant.Point) error
}

// WriteInitialIndex does not stage or commit a publication. Stage the prepared
// ExpectedBackend alongside all other required backends before calling it.
func WriteInitialIndex(ctx context.Context, store InitialIndexStore, backend InitialIndexBackend, p *PreparedInitialIndex) error {
	if store == nil || backend == nil || p == nil || p.snapshot == nil || len(p.records) == 0 {
		return errors.New("prepared initial index and stores required")
	}
	b := p.binding
	physical := backend.Binding()
	if backend.Endpoint() != b.Endpoint || physical.Collection != b.Collection || physical.CorpusID != b.Generation.Meta.CorpusId || !proto.Equal(physical.Generation, b.Generation) {
		return errors.New("index backend differs from admitted catalog binding")
	}
	release, err := store.AcquireIndexWriteLock(ctx, b.PublicationID)
	if err != nil {
		return err
	}
	defer release()
	manifest, err := store.LoadPublicationManifest(ctx, b.PublicationID)
	if err != nil {
		return err
	}
	if err = domain.ValidateWire(manifest, domain.DefaultWireLimits); err != nil {
		return err
	}
	if manifest.Meta.RecordId != b.PublicationID || manifest.Meta.CorpusId != b.Generation.Meta.CorpusId || manifest.Fence != b.Fence || manifest.ParentRef != nil || len(manifest.Closures) != 0 || !proto.Equal(manifest.SnapshotRef, p.snapshot) {
		return errors.New("initial writer requires exact staged reservation without parent or closures")
	}
	expected := p.ExpectedBackend()
	found := false
	for _, generation := range manifest.BackendGenerations {
		if generation.Backend == pb.BackendKind_BACKEND_KIND_QDRANT {
			if !proto.Equal(generation, expected) {
				return errors.New("staged Qdrant write set differs from prepared content")
			}
			found = true
		}
	}
	if !found {
		return errors.New("Qdrant backend is not staged")
	}
	// Preflight each write/readback page before reserving IDs or touching Qdrant.
	// The smaller shared page cap satisfies both adapter budgets even for large vectors.
	pages := [][]*pb.IndexRecord{}
	page := []*pb.IndexRecord{}
	bytes := 0
	for _, record := range p.records {
		size := proto.Size(record)
		if size > 512<<10 {
			return errors.New("index record exceeds bounded readback page")
		}
		if len(page) == 64 || bytes+size > 512<<10 {
			pages = append(pages, page)
			page = nil
			bytes = 0
		}
		page = append(page, record)
		bytes += size
	}
	if len(page) > 0 {
		pages = append(pages, page)
	}
	if err = store.RegisterIndexGeneration(ctx, b); err != nil {
		return err
	}
	pointPages := make([][]qdrant.Point, 0, len(pages))
	for _, records := range pages {
		ids, e := store.ReserveIndexPoints(ctx, b, records)
		if e != nil {
			return e
		}
		if len(ids) != len(records) {
			return errors.New("catalog returned incomplete point assignment")
		}
		points := make([]qdrant.Point, len(records))
		for i, record := range records {
			identity, e := domain.IndexPointIdentity(b.Generation.Meta.CorpusId, b.Generation.Meta.RecordId, record.Meta.RecordId)
			if e != nil || identity != ids[i] {
				return errors.New("catalog point assignment does not match record")
			}
			points[i] = qdrant.Point{ID: ids[i].PointID, Record: record}
		}
		pointPages = append(pointPages, points)
	}
	keyHash := sha256.Sum256([]byte("regulagraph-initial-index-operation-v1\x00" + b.PublicationID))
	key := fmt.Sprintf("index-write:%x", keyHash)
	if err = store.EnsureIndexWriteIntent(ctx, b, key, p.digest, uint64(len(p.records))); err != nil {
		return err
	}
	if err = backend.EnsureCollection(ctx); err != nil {
		return err
	}
	for _, points := range pointPages {
		if err = backend.Upsert(ctx, points); err != nil {
			return err
		}
	}
	for _, points := range pointPages {
		if err = backend.VerifyPoints(ctx, points); err != nil {
			return err
		}
	}
	if err = backend.VerifyInitialServing(ctx, uint64(len(p.records)), p.snapshot.Sequence, pointPages[0][0]); err != nil {
		return err
	}
	if err = store.RecordPublicationOperation(ctx, b.PublicationID, pb.BackendKind_BACKEND_KIND_QDRANT, key, p.digest, "applied", b.Fence); err != nil {
		return err
	}
	return store.RecordBackendReceipt(ctx, &pb.BackendReceipt{PublicationId: b.PublicationID,
		Backend: pb.BackendKind_BACKEND_KIND_QDRANT, Generation: expected.Generation, OperationsChecksum: expected.OperationsChecksum,
		Counts: expected.ExpectedCounts, DurableAck: true, SearchReady: true, Fence: b.Fence})
}
