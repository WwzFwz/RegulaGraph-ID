// Exercises PostgreSQL graph admission and Neo4j typed reads after actual combined
// publication. Scope, historical index-only snapshot and expired/released pin are
// rejected. Source support bytes remain exact; no traversal or legal inference is
// claimed by these record-read tests. The caller owns cleanup of fixture databases.
package indexing

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func checkPublishedGraphRead(t *testing.T, ctx context.Context, repo *postgres.Repository, backend *neo4j.Store, basePin domain.SnapshotPin, scope string, delta *pb.GraphDelta) {
	t.Helper()
	if _, err := repo.LoadPinnedGraph(ctx, basePin, scope); err == nil {
		t.Fatal("index-only parent admitted graph")
	}
	pin, err := repo.PinActiveSnapshot(ctx, basePin.CorpusID, "read:graph:"+basePin.CorpusID, "reader:graph", 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), pin.LeaseID, pin.OwnerID)
	if _, err = repo.LoadPinnedGraph(ctx, pin, "scope:other"); err == nil {
		t.Fatal("graph scope boundary ignored")
	}
	view, err := repo.LoadPinnedGraph(ctx, pin, scope)
	if err != nil {
		t.Fatal("published graph admission", err)
	}
	reader, err := backend.OpenReader(ctx, view)
	if err != nil {
		t.Fatal("published Neo4j admission", err)
	}
	for _, selection := range []struct {
		kind     string
		id       string
		expected proto.Message
	}{{"entity", delta.Entities[0].Meta.RecordId, delta.Entities[0]}, {"assertion", delta.Assertions[0].Meta.RecordId, delta.Assertions[0]}, {"support", delta.Supports[0].Meta.RecordId, delta.Supports[0]}, {"mention", delta.Mentions[0].Meta.RecordId, delta.Mentions[0]}, {"decision", delta.Decisions[0].Meta.RecordId, delta.Decisions[0]}} {
		values, e := reader.ReadRecords(ctx, selection.kind, []string{selection.id}, 1<<20)
		if e != nil || len(values) != 1 || !proto.Equal(values[0], selection.expected) {
			t.Fatal("source-bound typed read", selection.kind, e)
		}
	}
	if _, err = repo.LoadPinnedGraph(ctx, pin, scope); err != nil {
		t.Fatal("final graph lease recheck", err)
	}
	if err = repo.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.LoadPinnedGraph(ctx, pin, scope); err == nil {
		t.Fatal("released graph lease still authorized")
	}
	t.Log("published graph typed reads preserve canonical, assertion and source support bytes under scope/pin")
}
