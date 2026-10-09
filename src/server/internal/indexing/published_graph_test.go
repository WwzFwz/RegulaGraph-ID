// Exercises PostgreSQL graph admission and Neo4j typed reads after actual combined
// publication. Scope, historical index-only snapshot and expired/released pin are
// rejected. Production discovery traversal retains exact source support bytes;
// this does not prove legal applicability or graph-to-answer integration. The
// caller owns cleanup of fixture databases.
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
	graphretrieval "regulagraph.local/server/internal/retrieval/graph"
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
	paths, err := graphretrieval.Traverse(ctx, reader, view.Snapshot, []string{delta.Assertions[0].SubjectId}, graphretrieval.TraversalConfig{MaximumHops: 3, MaximumPaths: 100, Read: domain.GraphReadLimits{Assertions: 128, Supports: 256, Bytes: 1 << 20}})
	if err != nil || len(paths.Paths) == 0 || !proto.Equal(paths.Snapshot, view.Snapshot) {
		t.Fatal("published native graph traversal", paths, err)
	}
	for _, path := range paths.Paths {
		if err = domain.ValidateWire(path, domain.DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
		for i, assertionID := range path.OrderedAssertionIds {
			if paths.Supports[path.SelectedSupportIds[i]].AssertionId != assertionID {
				t.Fatal("native traversal lost source support")
			}
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
	t.Logf("production graph traversal on actual Rust output returned %d source-supported discovery paths", len(paths.Paths))
}
