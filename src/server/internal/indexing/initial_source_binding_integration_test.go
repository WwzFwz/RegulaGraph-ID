// Exercises unpublished-source binding through real PostgreSQL and FileStore.
// Original CHUNK checkpoint bytes remain authoritative; interrupted registration
// cannot grant a derived artifact authority. Replay, scope drift, cancellation
// and immutable receipts are tested before normal INDEX planning consumes it.
package indexing

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

type interruptedSourceBinding struct {
	*postgres.Repository
	fail     bool
	captured domain.IndexSourceBinding
}

func (s *interruptedSourceBinding) RegisterIndexSourceBinding(ctx context.Context, binding domain.IndexSourceBinding) error {
	s.captured = binding
	if s.fail {
		s.fail = false
		return errors.New("injected source binding interruption")
	}
	return s.Repository.RegisterIndexSourceBinding(ctx, binding)
}

func TestInitialSnapshotSourceBindingAgainstPostgres(t *testing.T) {
	runInitialIndexPublication(t, false, true, "source-binding")
}

func checkSnapshotSourceBinding(t *testing.T, ctx context.Context, repo *postgres.Repository, conn *pgx.Conn, publication string, fence uint64, snapshot *pb.SnapshotRef, job string, original *pb.ArtifactRef, artifacts indexMemoryArtifacts, scope string) (*pb.ArtifactRef, *pb.DocumentBatch) {
	t.Helper()
	files, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	store := &interruptedSourceBinding{Repository: repo, fail: true}
	input := InitialIndexSource{SourceJobID: job, DocumentBatch: original}
	if _, err = BindInitialIndexSource(ctx, store, artifacts, files, publication, fence, snapshot, scope, input); err == nil {
		t.Fatal("interrupted receipt accepted")
	}
	if err = repo.VerifyIndexSourceCheckpoint(ctx, snapshot.CorpusId, job, store.captured.Bound); err == nil {
		t.Fatal("uncommitted derived source gained authority")
	}
	// Simulate a previously staged publication with conflicting metadata before
	// any receipt exists; the reverse ordering is checked below through StagePublication.
	if _, err = conn.Exec(ctx, `UPDATE snapshots SET representation_generation='generation:other' WHERE publication_id=$1`, publication); err != nil {
		t.Fatal(err)
	}
	if err = repo.RegisterIndexSourceBinding(ctx, store.captured); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("binding ignored staged snapshot", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE snapshots SET representation_generation=NULL WHERE publication_id=$1`, publication); err != nil {
		t.Fatal(err)
	}
	result, err := BindInitialIndexSource(ctx, store, artifacts, files, publication, fence, snapshot, scope, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := BindInitialIndexSource(ctx, store, artifacts, files, publication, fence, snapshot, scope, input)
	if err != nil || !proto.Equal(result.DocumentBatch, replay.DocumentBatch) {
		t.Fatal("binding replay drift", err)
	}
	if err = repo.VerifyIndexSourceCheckpoint(ctx, snapshot.CorpusId, job, result.DocumentBatch); err != nil {
		t.Fatal("bound source rejected", err)
	}
	if err = repo.VerifyIndexSourceCheckpoint(ctx, snapshot.CorpusId, job, original); err != nil {
		t.Fatal("original authority lost", err)
	}
	if err = repo.VerifyIndexSourceCheckpoint(ctx, snapshot.CorpusId, "job:foreign", result.DocumentBatch); err == nil {
		t.Fatal("bound source changed owner")
	}
	changed := proto.Clone(result.DocumentBatch).(*pb.ArtifactRef)
	changed.StorageKey += ".wrong"
	if err = repo.VerifyIndexSourceCheckpoint(ctx, snapshot.CorpusId, job, changed); err == nil {
		t.Fatal("bound reference drift accepted")
	}
	bad := store.captured
	bad.Fence++
	if err = repo.RegisterIndexSourceBinding(ctx, bad); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("stale publisher binding", err)
	}
	bad = store.captured
	bad.AuthScope += "-changed"
	if err = repo.RegisterIndexSourceBinding(ctx, bad); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("source scope drift", err)
	}
	wrongSnapshot := proto.Clone(snapshot).(*pb.SnapshotRef)
	wrongSnapshot.RepresentationGeneration = "generation:other"
	manifest := &pb.PublicationManifest{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: snapshot.CorpusId, RecordId: publication}, SnapshotRef: wrongSnapshot, Fence: fence,
		BackendGenerations: []*pb.BackendGeneration{{Backend: pb.BackendKind_BACKEND_KIND_QDRANT, Generation: "generation:other", OperationsChecksum: snapshot.ManifestHash, ExpectedCounts: &pb.Counts{Expected: 1, Accepted: 1}}}, ValidationReport: &pb.ValidationReport{Valid: true, CheckedRecords: 1}}
	if err = repo.StagePublication(ctx, manifest); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("stage ignored bound snapshot", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if err = repo.VerifyIndexSourceCheckpoint(ctx, snapshot.CorpusId, job, result.DocumentBatch); err == nil {
		t.Fatal("cancelled source authorized")
	}
	if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE index_source_bindings SET source_job_id=source_job_id WHERE publication_id=$1`, publication); err == nil {
		t.Fatal("binding receipt mutable")
	}
	raw, err := files.ReadVerified(ctx, result.DocumentBatch, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	artifacts[result.DocumentBatch.ArtifactId] = raw
	bound := new(pb.DocumentBatch)
	if err = domain.DecodeWire(raw, bound, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(bound.Context.SnapshotRef, snapshot) {
		t.Fatal("bound snapshot differs")
	}
	return result.DocumentBatch, bound
}
