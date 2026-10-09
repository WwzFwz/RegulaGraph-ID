// Injects selection faults only inside the disposable indexing test schema.
// Extra unindexed receipts must not starve exact INDEX members; oversized and
// missing member references must fail closed. Production immutable triggers are
// restored before the actual preparation command and worker run.
package indexing

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func checkGraphSourceSelectionFaults(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, source domain.IndexSourceBinding) {
	t.Helper()
	pin, err := repo.PinActiveSnapshot(ctx, source.Snapshot.CorpusId, "pin:selection-test", "owner:selection-test", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), pin.LeaseID, pin.OwnerID)
	// Simulate other source receipts prepared under this publication but never
	// selected for its index. Their IDs deliberately sort before the real job.
	_, err = db.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload)
 SELECT '000-extra-'||n,j.corpus_id,j.operation,j.state,j.stage,j.input_fingerprint,'000-extra-'||n,j.request_hash,j.request_payload
 FROM jobs j CROSS JOIN generate_series(1,257) n WHERE j.job_id=$1`, source.SourceJobID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO artifacts(artifact_id,corpus_id,algorithm,digest,storage_key,media_type,byte_size,schema_version)
 SELECT '000-extra-bound-'||n,a.corpus_id,a.algorithm,md5(n::text)||md5(n::text),'extra-bound-'||n,a.media_type,a.byte_size,a.schema_version
 FROM artifacts a CROSS JOIN generate_series(1,257) n WHERE a.artifact_id=$1`, source.Bound.ArtifactId)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO index_source_bindings(publication_id,source_job_id,original_artifact_id,bound_artifact_id,original_reference,bound_reference)
 SELECT publication_id,'000-extra-'||n,original_artifact_id,'000-extra-bound-'||n,original_reference,bound_reference
 FROM index_source_bindings CROSS JOIN generate_series(1,257) n WHERE publication_id=$1 AND source_job_id=$2`, source.PublicationID, source.SourceJobID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := repo.LoadPublishedGraphSources(ctx, pin, source.AuthScope)
	if err != nil || len(selected) != 1 || selected[0].SourceJobID != source.SourceJobID {
		t.Fatal("unindexed receipts hid valid member", err)
	}
	var original []byte
	if err = db.QueryRow(ctx, `SELECT original_reference FROM index_source_bindings WHERE publication_id=$1 AND source_job_id=$2`, source.PublicationID, source.SourceJobID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `ALTER TABLE index_source_bindings DISABLE TRIGGER index_source_binding_immutable`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := db.Exec(ctx, `UPDATE index_source_bindings SET original_reference=$3 WHERE publication_id=$1 AND source_job_id=$2`, source.PublicationID, source.SourceJobID, original); e != nil {
			t.Error(e)
		}
		if _, e := db.Exec(ctx, `ALTER TABLE index_source_bindings ENABLE TRIGGER index_source_binding_immutable`); e != nil {
			t.Error(e)
		}
	}()
	if _, err = db.Exec(ctx, `UPDATE index_source_bindings SET original_reference=decode(repeat('ab',65537),'hex') WHERE publication_id=$1 AND source_job_id=$2`, source.PublicationID, source.SourceJobID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.LoadPublishedGraphSources(ctx, pin, source.AuthScope); err == nil {
		t.Fatal("oversized source reference admitted")
	}
	if _, err = db.Exec(ctx, `UPDATE index_source_bindings SET original_reference=$3 WHERE publication_id=$1 AND source_job_id=$2`, source.PublicationID, source.SourceJobID, original); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.LoadPublishedGraphSources(ctx, pin, source.AuthScope); err != nil {
		t.Fatal("restored source unreadable", err)
	}
	t.Log("source selection: 257 nonmembers ignored; oversized member rejected; exact restored selection admitted")
}
