// Exercises PostgreSQL graph admission and Neo4j typed reads after actual combined
// publication. Scope, historical index-only snapshot and expired/released pin are
// rejected. Production discovery traversal retains exact source support bytes;
// graph-to-draft rendering/citations are exercised by the companion helper;
// this does not prove legal applicability or model quality. The
// caller owns cleanup of fixture databases.
package indexing

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	graphretrieval "regulagraph.local/server/internal/retrieval/graph"
	"regulagraph.local/server/internal/workflows"
)

func checkPublishedGraphRead(t *testing.T, ctx context.Context, repo *postgres.Repository, backend *neo4j.Store, basePin domain.SnapshotPin, scope string, delta *pb.GraphDelta, artifacts indexMemoryArtifacts) {
	t.Helper()
	if _, err := repo.LoadPinnedGraph(ctx, basePin, scope); err == nil {
		t.Fatal("index-only parent admitted graph")
	}
	readLease := 20 * time.Second
	if os.Getenv("REGULAGRAPH_TEST_LLAMA_GRAPH_ANSWER") == "1" {
		readLease = 4 * time.Minute
	}
	pin, err := repo.PinActiveSnapshot(ctx, basePin.CorpusID, "read:graph:"+basePin.CorpusID, "reader:graph", readLease)
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
	checkPublishedGraphEvidence(t, ctx, repo, backend, pin, scope, paths, artifacts, delta.Supports[0].ExtractionManifest)
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

func checkPublishedGraphEvidence(t *testing.T, ctx context.Context, repo *postgres.Repository, backend *neo4j.Store, pin domain.SnapshotPin, scope string, paths *graphretrieval.TraversalResult, artifacts indexMemoryArtifacts, producer *pb.ProducerManifest) {
	t.Helper()
	index, err := repo.LoadPinnedIndex(ctx, pin)
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := qdrant.New(index.Binding.Endpoint, "", &http.Client{Timeout: 5 * time.Second}, qdrant.Binding{Collection: index.Binding.Collection, CorpusID: index.Snapshot.CorpusId, Generation: index.Binding.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if err = lookup.OpenExistingCollection(ctx); err != nil {
		t.Fatal(err)
	}
	h := &workflows.GraphEvidenceHydrator{Catalog: repo, Artifacts: artifacts, Lookup: lookup, Config: retrieval.HydrationConfig{MaximumCandidates: 256, MaximumArtifactBytes: 16 << 20, MaximumEvidenceBytes: 1 << 20, Producer: producer}}
	request := &pb.QuestionRequest{Question: "Apa dasar hubungan perizinan?", CorpusId: pin.CorpusID, RequestedProfile: pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE, TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}
	got, err := h.Hydrate(ctx, request, index, scope, paths)
	if err != nil {
		t.Fatal("native graph source hydration", err)
	}
	if len(got.Mapping.Bundle.Items) == 0 || len(got.Mapping.SupportEvidence) == 0 || len(got.Mapping.PathEvidence) == 0 || len(got.Hits) == 0 {
		t.Fatal("native graph lost source evidence", got)
	}
	if !proto.Equal(got.Mapping.Bundle.Snapshot, paths.Snapshot) {
		t.Fatal("graph source snapshot changed")
	}
	for _, item := range got.Mapping.Bundle.Items {
		if item.Text == "" || len(item.GraphPaths) == 0 || len(item.SourceRefs) == 0 {
			t.Fatal("graph evidence lacks authenticated text/path", item)
		}
	}
	if got.Mapping.Bundle.Completeness != pb.Completeness_COMPLETENESS_PARTIAL {
		t.Fatal("unresolved fixture applicability hidden")
	}
	if _, err = h.Hydrate(ctx, request, index, "scope:other", paths); err == nil {
		t.Fatal("graph source hydration bypassed scope")
	}
	checkPublishedGraphAnswer(t, ctx, repo, backend, index, scope, request, paths, got, artifacts)
	// Revoke a separate real pin during the workflow's final admission read:
	// successful earlier hydration must not authorize returning stale output.
	revoked, err := repo.PinActiveSnapshot(ctx, pin.CorpusID, "read:graph:revoke:"+pin.CorpusID, "reader:revoke", 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), revoked.LeaseID, revoked.OwnerID)
	revokedIndex, err := repo.LoadPinnedIndex(ctx, revoked)
	if err != nil {
		t.Fatal(err)
	}
	gate := &revokingGraphCatalog{Repository: repo}
	h.Catalog = gate
	if _, err = h.Hydrate(ctx, request, revokedIndex, scope, paths); err == nil || gate.calls != 2 {
		t.Fatal("final graph pin revocation ignored", gate.calls, err)
	}
	t.Logf("actual graph -> Qdrant source lookup -> PostgreSQL/artifact hydration: %d evidence items, %d mapped paths; unresolved applicability stays partial", len(got.Mapping.Bundle.Items), len(got.Mapping.PathEvidence))
}

type revokingGraphCatalog struct {
	*postgres.Repository
	calls int
}

func (r *revokingGraphCatalog) LoadPinnedGraph(ctx context.Context, pin domain.SnapshotPin, scope string) (*domain.PinnedGraph, error) {
	r.calls++
	if r.calls == 2 {
		if err := r.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID); err != nil {
			return nil, err
		}
	}
	return r.Repository.LoadPinnedGraph(ctx, pin, scope)
}
