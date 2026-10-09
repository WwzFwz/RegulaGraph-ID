// Extends the real Rust/PG graph pipeline to actual Neo4j writes and readback.
// Output bytes were already admitted by the production coordinator; this helper
// tests backend projection interoperability, not PostgreSQL publication or graph
// retrieval. It uses a dedicated corpus/generation and deletes only its own data.
// A missing Neo4j test URI skips this subtest explicitly, not the RPC/PG test.
package indexing

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
)

func checkNativeGraphNeo4j(t *testing.T, ctx context.Context, plan *pb.GraphAssemblyPlan, delta *pb.GraphDelta) {
	t.Helper()
	uri := os.Getenv("REGULAGRAPH_TEST_NEO4J_URI")
	if uri == "" {
		t.Skip("disposable Neo4j required for graph backend proof")
	}
	password := os.Getenv("REGULAGRAPH_TEST_NEO4J_PASSWORD")
	if password == "" {
		t.Fatal("Neo4j test password required")
	}
	binding := neo4j.Binding{CorpusID: plan.Meta.CorpusId, Generation: "graph-generation:" + plan.PublicationId, PublicationID: plan.PublicationId,
		Fence: plan.PublicationFence, Sequence: plan.TargetSequence, RegistryRevision: plan.RegistryRevision, BaseSnapshot: plan.Context.SnapshotRef}
	store, err := neo4j.New(neo4j.Config{URI: uri, Username: "neo4j", Password: password, Database: "neo4j", PoolSize: 2, Timeout: 20 * time.Second}, binding)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		driver, e := bolt.NewDriverWithContext(uri, bolt.BasicAuth("neo4j", password, ""))
		if e != nil {
			t.Error(e)
			return
		}
		defer driver.Close(cleanup)
		session := driver.NewSession(cleanup, bolt.SessionConfig{DatabaseName: "neo4j"})
		defer session.Close(cleanup)
		result, e := session.Run(cleanup, `MATCH (n) WHERE n.corpus=$corpus AND n.generation=$generation DETACH DELETE n`, map[string]any{"corpus": binding.CorpusID, "generation": binding.Generation})
		if e == nil {
			_, e = result.Consume(cleanup)
		}
		if e != nil {
			t.Error("Neo4j fixture cleanup", e)
		}
	}()
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = store.ApplyGraphDelta(ctx, delta); err != nil {
			t.Fatal("actual Rust delta Neo4j write/replay", err)
		}
	}
	proof, err := store.VerifyAndSeal(ctx, []*pb.GraphDelta{delta})
	expected := uint64(len(delta.Entities) + len(delta.Mentions) + len(delta.Assertions) + len(delta.Supports) + len(delta.Decisions))
	if err != nil || proof.Records != expected || proof.Edges != 5 || proof.Operations != 1 {
		t.Fatal("actual Rust delta Neo4j readback", proof, err)
	}
	t.Log(fmt.Sprintf("actual Rust -> PostgreSQL STAGED -> Neo4j verified: records=%d, edges=%d; PostgreSQL publication still separate", proof.Records, proof.Edges))
}
