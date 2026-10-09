// Extends the real Rust/PG graph pipeline to actual Neo4j writes and readback.
// Output bytes were already admitted by the production coordinator; this helper
// tests projection, receipts, actual publication and unchanged-index hydration.
// Graph traversal and model quality remain outside this fixture. It uses a
// dedicated corpus/generation and deletes only its own data after query checks.
// A missing Neo4j test URI skips this subtest explicitly, not the RPC/PG test.
package indexing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkNativeGraphNeo4j(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission, pin domain.SnapshotPin, prepared *workflows.PreparedGraphOutputs, artifacts indexMemoryArtifacts) {
	t.Helper()
	plan, delta := prepared.Completed().Inventory.Assignments[0].Plan, prepared.Deltas()[0]
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
			return
		}
		// Fixture-only compensation after deletion/readback. Never mark a real
		// backend compensated merely to make AbortPublication accept its ledger.
		result, e = session.Run(cleanup, `MATCH (n) WHERE n.corpus=$corpus AND n.generation=$generation RETURN count(n)`, map[string]any{"corpus": binding.CorpusID, "generation": binding.Generation})
		if e != nil {
			t.Error(e)
			return
		}
		row, e := result.Single(cleanup)
		if e != nil || row.Values[0] != int64(0) {
			t.Error("fixture graph cleanup incomplete", e)
			return
		}
		catalog, e := repo.LoadGraphGeneration(cleanup, binding.CorpusID, binding.PublicationID)
		if errors.Is(e, domain.ErrNotFound) {
			return
		}
		if e != nil {
			t.Error(e)
			return
		}
		var state int16
		if e = db.QueryRow(cleanup, `SELECT state FROM snapshots WHERE publication_id=$1`, binding.PublicationID).Scan(&state); e != nil {
			t.Error(e)
			return
		}
		if state == int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED) {
			return
		} // isolated fixture database is dropped after query checks
		if e = repo.RecordPublicationOperation(cleanup, binding.PublicationID, pb.BackendKind_BACKEND_KIND_NEO4J, postgres.GraphWriteOperation(binding.PublicationID), catalog.OperationsHash, "compensated", binding.Fence); e != nil {
			t.Error("fixture compensation ledger", e)
		}
	}()
	proof := checkGraphCatalogWrite(t, ctx, repo, db, authority, pin, prepared, store)
	expected := uint64(len(delta.Entities) + len(delta.Mentions) + len(delta.Assertions) + len(delta.Supports) + len(delta.Decisions))
	if err != nil || proof.Records != expected || proof.Edges != 5 || proof.Operations != 1 {
		t.Fatal("actual Rust delta Neo4j readback", proof, err)
	}
	checkReusedGraphPublication(t, ctx, repo, db, pin, binding.PublicationID, artifacts, plan.ProducerManifest)
	t.Log(fmt.Sprintf("actual Rust -> PostgreSQL STAGED -> Neo4j/Qdrant receipt -> published snapshot -> cited draft: records=%d, edges=%d", proof.Records, proof.Edges))
}
