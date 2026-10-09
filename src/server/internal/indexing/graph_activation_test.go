// Isolates final graph authority checks inside the real PostgreSQL CAS path.
// Actual Qdrant readback/reuse opens the earlier readiness gate. A test-schema
// trigger aborts at the publication UPDATE to isolate stale-authority rejections.
// Successful activation is tested afterward by the native graph caller. Model
// inputs remain synthetic; this does not prove retrieval quality or benchmarks.
package indexing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkGraphActivationAuthority(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission, pin domain.SnapshotPin, prepared *workflows.PreparedGraphOutputs, backend *neo4j.Store) {
	t.Helper()
	pub := backend.Binding().PublicationID
	checkIndexReuseReadiness(t, ctx, repo, db, authority, pin, pub)
	var err error
	// The schema is private to this test. No successful activation is permitted.
	if _, err = db.Exec(ctx, `CREATE FUNCTION block_graph_activation_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'graph-guard-passed-fixture'; END $$;
 CREATE TRIGGER graph_activation_fixture BEFORE UPDATE OF state ON snapshots FOR EACH ROW WHEN (NEW.state=4) EXECUTE FUNCTION block_graph_activation_fixture()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := db.Exec(ctx, `DROP TRIGGER graph_activation_fixture ON snapshots; DROP FUNCTION block_graph_activation_fixture()`); e != nil {
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
	var indexJob string
	if err = db.QueryRow(ctx, `SELECT a.job_id FROM snapshot_index_reuse r JOIN index_job_assignments a ON a.publication_id=r.source_publication_id WHERE r.publication_id=$1 ORDER BY a.ordinal LIMIT 1`, pub).Scan(&indexJob); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, indexJob); err != nil {
		t.Fatal(err)
	}
	checkReject()
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, indexJob); err != nil {
		t.Fatal(err)
	}
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
