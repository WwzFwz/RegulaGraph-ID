// Publishes the complete durable first-snapshot dense/BM25 inventory through the
// existing admission, Qdrant readback and PostgreSQL pointer protocol. This is an
// explicit vector/lexical profile: an already-staged graph requirement cannot be
// removed on retry. No backend mutation occurs until all child outputs pass.
// Failures preserve intents for replay; published replay never repeats writes.
// Record prepare/write/readiness/commit timings under benchmark-targets.yaml;
// publication integrity is not model accuracy or Hybrid GraphRAG acceptance.
package indexing

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type CompletedIndexPublicationStore interface {
	CompletedIndexStore
	PublicationStore
	InitialIndexStore
}

func PublishCompletedVectorIndex(ctx context.Context, store CompletedIndexPublicationStore, reader IndexArtifactReader, backend InitialIndexBackend, publication string) (*pb.SnapshotRef, error) {
	if ctx == nil || store == nil || reader == nil || backend == nil {
		return nil, errors.New("initial publication dependencies required")
	}
	p, err := PrepareCompletedInitialIndex(ctx, store, reader, publication)
	if err != nil {
		return nil, err
	}
	coordinator, err := NewPublicationCoordinator(store)
	if err != nil {
		return nil, err
	}
	reservation, err := coordinator.Reserve(ctx, publication, "", p.snapshot.CorpusId, p.snapshot.SnapshotId, "")
	if err != nil {
		return nil, err
	}
	if reservation.Fence != p.binding.Fence || reservation.Sequence != p.snapshot.Sequence {
		return nil, errors.New("initial publication reservation changed")
	}
	manifest := &pb.PublicationManifest{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: p.snapshot.CorpusId, RecordId: publication}, SnapshotRef: proto.Clone(p.snapshot).(*pb.SnapshotRef), Fence: p.binding.Fence,
		BackendGenerations: []*pb.BackendGeneration{p.ExpectedBackend()}, ValidationReport: &pb.ValidationReport{Valid: true, CheckedRecords: uint64(len(p.records))}}
	if reservation.State == pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED {
		stored, e := store.LoadPublicationManifest(ctx, publication)
		if e != nil {
			return nil, e
		}
		stored = proto.Clone(stored).(*pb.PublicationManifest)
		stored.Acknowledgements = nil
		if !proto.Equal(stored, manifest) {
			return nil, errors.New("published manifest differs from admitted vector profile")
		}
		return proto.Clone(p.snapshot).(*pb.SnapshotRef), nil
	}
	if err = coordinator.Stage(ctx, reservation, manifest); err != nil {
		return nil, err
	}
	if err = WriteInitialIndex(ctx, store, backend, p); err != nil {
		return nil, err
	}
	if err = coordinator.Publish(ctx, publication); err != nil {
		return nil, err
	}
	return proto.Clone(p.snapshot).(*pb.SnapshotRef), nil
}
