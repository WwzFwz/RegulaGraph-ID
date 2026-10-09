// Publishes an admitted whole graph generation with a fully verified inherited
// dense/BM25 index. Manifest preparation is deterministic; existing requirements
// cannot be removed on replay. Each backend readback precedes durable receipt,
// then the existing publication guard performs active-pointer CAS. Failures leave
// retryable intents and never invent rollback across storage systems. This is
// graph addition to unchanged source membership, not incremental closure.
// Measure read/hash/write/seal/receipt/CAS p95 and RSS; benchmark-targets.yaml
// remains the sole acceptance target, independent of fixture correctness.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

type GraphPublicationStore interface {
	PublicationStore
	IndexReuseStore
}
type GraphPublicationAuthority interface {
	GraphReadinessAuthority
	IndexReuseAuthority
}

// PublishPreparedGraph consumes source-authenticated output and an explicit
// target snapshot identity. No parsing/extraction/embedding happens here.
func PublishPreparedGraph(ctx context.Context, store GraphPublicationStore, authority GraphPublicationAuthority,
	graph GraphGenerationBackend, index IndexReuseBackend, prepared *workflows.PreparedGraphOutputs,
	pin domain.SnapshotPin, snapshotID string) (*pb.SnapshotRef, error) {
	if ctx == nil || store == nil || authority == nil || index == nil {
		return nil, errors.New("graph publication dependencies required")
	}
	ctx, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	b, err := DescribePreparedGraph(prepared, graph)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: pin.CorpusID, RecordId: snapshotID}, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	base, err := store.LoadPinnedIndex(ctx, pin)
	if err != nil {
		return nil, err
	}
	if base == nil || domain.ValidateWire(base.Snapshot, domain.DefaultWireLimits) != nil {
		return nil, domain.ErrPersistentIntegrity
	}
	if !proto.Equal(base.Snapshot, b.Binding.BaseSnapshot) || base.Snapshot.CorpusId != pin.CorpusID {
		return nil, errors.New("graph parent differs from pinned index")
	}
	physical := index.Binding()
	if index.Endpoint() != base.Binding.Endpoint || physical.Collection != base.Binding.Collection || physical.CorpusID != pin.CorpusID || !proto.Equal(physical.Generation, base.Binding.Generation) {
		return nil, errors.New("index backend differs from pinned route")
	}
	origin, err := store.LoadPublicationManifest(ctx, base.Binding.PublicationID)
	if err != nil {
		return nil, err
	}
	manifest, err := graphPublicationManifest(b, origin, snapshotID)
	if err != nil {
		return nil, err
	}
	coordinator, _ := NewPublicationCoordinator(store)
	reservation, err := coordinator.Reserve(ctx, b.Binding.PublicationID, "", pin.CorpusID, snapshotID, base.Snapshot.SnapshotId)
	if err != nil {
		return nil, err
	}
	if reservation.Fence != b.Binding.Fence || reservation.Sequence != b.Binding.Sequence {
		return nil, errors.New("graph reservation changed")
	}
	if reservation.State == pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED {
		stored, e := store.LoadPublicationManifest(ctx, b.Binding.PublicationID)
		if e != nil {
			return nil, e
		}
		if e = domain.ValidateWire(stored, domain.DefaultWireLimits); e != nil {
			return nil, errors.Join(domain.ErrPersistentIntegrity, e)
		}
		stored = proto.Clone(stored).(*pb.PublicationManifest)
		stored.Acknowledgements = nil
		if !proto.Equal(stored, manifest) {
			return nil, errors.New("published graph profile differs")
		}
		return proto.Clone(manifest.SnapshotRef).(*pb.SnapshotRef), nil
	}
	if err = prepared.Revalidate(ctx, authority, pin); err != nil {
		return nil, err
	}
	if err = coordinator.Stage(ctx, reservation, manifest); err != nil {
		return nil, err
	}
	if _, err = AcknowledgePreparedGraph(ctx, authority, graph, prepared, pin); err != nil {
		return nil, err
	}
	if _, err = AcknowledgeIndexReuse(ctx, store, authority, index, pin, b.Binding.PublicationID); err != nil {
		return nil, err
	}
	if err = coordinator.Publish(ctx, b.Binding.PublicationID); err != nil {
		return nil, err
	}
	return proto.Clone(manifest.SnapshotRef).(*pb.SnapshotRef), nil
}

func graphPublicationManifest(b domain.GraphCatalogBinding, origin *pb.PublicationManifest, snapshotID string) (*pb.PublicationManifest, error) {
	if err := domain.ValidateGraphCatalogBinding(b); err != nil {
		return nil, err
	}
	if err := domain.ValidateWire(origin, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if origin.Meta.CorpusId != b.Binding.CorpusID || origin.SnapshotRef.Sequence > b.Binding.BaseSnapshot.Sequence {
		return nil, errors.New("foreign or future index origin")
	}
	var inherited *pb.BackendGeneration
	for _, g := range origin.BackendGenerations {
		if g.Backend == pb.BackendKind_BACKEND_KIND_QDRANT {
			if inherited != nil {
				return nil, errors.New("duplicate index origin")
			}
			inherited = proto.Clone(g).(*pb.BackendGeneration)
		}
	}
	if inherited == nil {
		return nil, errors.New("published index origin required")
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	target := proto.Clone(b.Binding.BaseSnapshot).(*pb.SnapshotRef)
	target.SnapshotId = snapshotID
	target.Sequence = b.Binding.Sequence
	target.ManifestHash = &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	m := &pb.PublicationManifest{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: b.Binding.CorpusID, RecordId: b.Binding.PublicationID}, SnapshotRef: target, ParentRef: proto.Clone(b.Binding.BaseSnapshot).(*pb.SnapshotRef), Fence: b.Binding.Fence, BackendGenerations: []*pb.BackendGeneration{b.ExpectedBackend(), inherited}, ValidationReport: &pb.ValidationReport{Valid: true, CheckedRecords: b.Records + b.Edges}}
	if err = domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	return m, nil
}
