// Isolates final graph authority checks inside the real PostgreSQL CAS path.
// A SYNTHETIC Qdrant receipt opens the earlier generic readiness gate, while a
// target-only trigger aborts at the publication UPDATE after the graph guard.
// All CAS changes roll back; this does not prove index carry-forward or successful
// Hybrid GraphRAG activation. Native Neo4j readiness itself comes from real I/O.
package indexing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkGraphActivationAuthority(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission, pin domain.SnapshotPin, prepared *workflows.PreparedGraphOutputs, backend *neo4j.Store) {
	t.Helper()
	pub := backend.Binding().PublicationID
	manifest, err := repo.LoadPublicationManifest(ctx, pub)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range manifest.BackendGenerations {
		if b.Backend == pb.BackendKind_BACKEND_KIND_QDRANT {
			r := &pb.BackendReceipt{PublicationId: pub, Backend: b.Backend, Fence: manifest.Fence, Generation: b.Generation, OperationsChecksum: proto.Clone(b.OperationsChecksum).(*pb.ContentHash), Counts: proto.Clone(b.ExpectedCounts).(*pb.Counts), DurableAck: true, SearchReady: true}
			if err = repo.RecordBackendReceipt(ctx, r); err != nil {
				t.Fatal("synthetic receipt fixture", err)
			}
		}
	}
	// The schema is private to this test. No successful activation is permitted.
	if _, err = db.Exec(ctx, `CREATE FUNCTION block_graph_activation_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'graph-guard-passed-fixture'; END $$;
 CREATE TRIGGER graph_activation_fixture BEFORE UPDATE OF state ON snapshots FOR EACH ROW WHEN (NEW.state=4) EXECUTE FUNCTION block_graph_activation_fixture()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := db.Exec(ctx, `DROP TRIGGER graph_activation_fixture ON snapshots; DROP FUNCTION block_graph_activation_fixture()`); e != nil {
			t.Error(e)
		}
		if _, e := db.Exec(ctx, `DELETE FROM backend_receipts WHERE publication_id=$1 AND backend=$2`, pub, int16(pb.BackendKind_BACKEND_KIND_QDRANT)); e != nil {
			t.Error(e)
		}
	}()
	checkPass := func() {
		t.Helper()
		e := repo.CommitPublication(ctx, pub)
		if e == nil || !strings.Contains(e.Error(), "graph-guard-passed-fixture") {
			t.Fatal("valid authority did not reach rollback sentinel", e)
		}
	}
	checkReject := func() {
		t.Helper()
		e := repo.CommitPublication(ctx, pub)
		if !errors.Is(e, postgres.ErrPublicationNotReady) {
			t.Fatal("stale graph authority reached activation", e)
		}
	}
	checkPass()
	assignment := prepared.Completed().Inventory.Assignments[0]
	for _, job := range []string{assignment.JobID, assignment.SourceJobID} {
		if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, job); err != nil {
			t.Fatal(err)
		}
		checkReject()
		if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, job); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision+1 WHERE corpus_id=$1`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	checkReject()
	if _, err = db.Exec(ctx, `UPDATE corpus_state SET registry_revision=registry_revision-1 WHERE corpus_id=$1`, pin.CorpusID); err != nil {
		t.Fatal(err)
	}
	lock, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, `SELECT job_id FROM jobs WHERE job_id=$1 FOR UPDATE`, assignment.JobID); err != nil {
		_ = lock.Rollback(ctx)
		t.Fatal(err)
	}
	checkReject()
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE graph_readiness_authority SET jobs_hash=repeat('0',64) WHERE publication_id=$1`, pub); err != nil {
		t.Fatal(err)
	}
	checkReject()
	if _, err = AcknowledgePreparedGraph(ctx, authority, backend, prepared, pin); err != nil {
		t.Fatal("refresh after fresh readback/admission", err)
	}
	checkPass()
	if _, err = db.Exec(ctx, `DELETE FROM graph_readiness_authority WHERE publication_id=$1`, pub); err != nil {
		t.Fatal(err)
	}
	checkReject()
	if _, err = AcknowledgePreparedGraph(ctx, authority, backend, prepared, pin); err != nil {
		t.Fatal("restore readiness authority", err)
	}
	checkPass()
}
