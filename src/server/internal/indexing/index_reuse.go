// Verifies the complete existing Qdrant generation for a new graph snapshot,
// without embedding, upsert or catalog ownership changes. Keyset pages are bounded;
// count and exact vector/payload readback plus target-visibility probes precede the
// source-admitted mapping/receipt transaction. Backend failure leaves no receipt.
// Query hydration uses the original build snapshot and the new visibility snapshot.
// Measure readback/receipt latency, bytes and RSS against benchmark-targets.yaml.
package indexing

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
)

type IndexReuseStore interface {
	LoadPinnedIndex(context.Context, domain.SnapshotPin) (*domain.PinnedIndex, error)
	LoadPublicationManifest(context.Context, string) (*pb.PublicationManifest, error)
	ReadPinnedIndexPage(context.Context, domain.SnapshotPin, string) ([]domain.IndexCatalogRecord, error)
}
type IndexReuseAuthority interface {
	RecordIndexReuse(context.Context, domain.SnapshotPin, domain.IndexReuseBinding) error
}
type IndexReuseBackend interface {
	Endpoint() string
	Binding() qdrant.Binding
	OpenExistingCollection(context.Context) error
	VerifyPoints(context.Context, []qdrant.Point) error
	VerifyInheritedServing(context.Context, uint64, uint64, qdrant.Point) error
}

func AcknowledgeIndexReuse(ctx context.Context, store IndexReuseStore, authority IndexReuseAuthority, backend IndexReuseBackend, pin domain.SnapshotPin, publication string) (*pb.BackendReceipt, error) {
	if ctx == nil || store == nil || authority == nil || backend == nil {
		return nil, errors.New("index reuse stores and context required")
	}
	ctx, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	base, err := store.LoadPinnedIndex(ctx, pin)
	if err != nil {
		return nil, err
	}
	target, err := store.LoadPublicationManifest(ctx, publication)
	if err != nil {
		return nil, err
	}
	origin, err := store.LoadPublicationManifest(ctx, base.Binding.PublicationID)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateWire(target, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if !proto.Equal(target.ParentRef, base.Snapshot) || len(target.Closures) != 0 {
		return nil, errors.New("reuse requires unchanged parent membership")
	}
	b := domain.IndexReuseBinding{PublicationID: publication, Fence: target.Fence, Target: target.SnapshotRef, Parent: base.Snapshot, Source: base.EvidenceSnapshot(), Index: base.Binding}
	for _, g := range origin.BackendGenerations {
		if g.Backend == pb.BackendKind_BACKEND_KIND_QDRANT {
			b.Expected = proto.Clone(g).(*pb.BackendGeneration)
		}
	}
	if err = domain.ValidateIndexReuseBinding(b); err != nil {
		return nil, err
	}
	matched := false
	for _, g := range target.BackendGenerations {
		if proto.Equal(g, b.Expected) {
			matched = true
		}
	}
	if !matched {
		return nil, errors.New("target index requirement differs from published origin")
	}
	physical := backend.Binding()
	if backend.Endpoint() != base.Binding.Endpoint || physical.Collection != base.Binding.Collection || physical.CorpusID != pin.CorpusID || !proto.Equal(physical.Generation, base.Binding.Generation) {
		return nil, errors.New("reuse backend route differs from catalog")
	}
	if err = backend.OpenExistingCollection(ctx); err != nil {
		return nil, err
	}
	cursor := ""
	count := uint64(0)
	var probe qdrant.Point
	for {
		page, e := store.ReadPinnedIndexPage(ctx, pin, cursor)
		if e != nil {
			return nil, e
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 64 {
			return nil, domain.ErrPersistentIntegrity
		}
		points := make([]qdrant.Point, 0, len(page))
		for _, item := range page {
			r := item.Record
			if r == nil || r.Meta == nil || r.Meta.RecordId == cursor || r.Meta.Visibility == nil || r.Meta.Visibility.FromSeq > b.Target.Sequence || r.Meta.Visibility.ToSeq != nil {
				return nil, domain.ErrPersistentIntegrity
			}
			count++
			if count > b.Expected.ExpectedCounts.Expected {
				return nil, domain.ErrPersistentIntegrity
			}
			points = append(points, qdrant.Point{ID: item.PointID, Record: r})
			cursor = r.Meta.RecordId
		}
		if probe.Record == nil {
			probe = points[0]
		}
		if err = backend.VerifyPoints(ctx, points); err != nil {
			return nil, err
		}
	}
	if count != b.Expected.ExpectedCounts.Expected {
		return nil, errors.New("reuse catalog coverage differs from published count")
	}
	if err = backend.VerifyInheritedServing(ctx, count, b.Target.Sequence, probe); err != nil {
		return nil, err
	}
	if err = authority.RecordIndexReuse(ctx, pin, b); err != nil {
		return nil, err
	}
	return &pb.BackendReceipt{PublicationId: publication, Fence: b.Fence, Backend: b.Expected.Backend, Generation: b.Expected.Generation, OperationsChecksum: b.Expected.OperationsChecksum, Counts: b.Expected.ExpectedCounts, DurableAck: true, SearchReady: true}, nil
}
