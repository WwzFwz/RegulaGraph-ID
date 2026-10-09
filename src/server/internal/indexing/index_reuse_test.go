// Verifies unchanged-index reuse with actual Qdrant/PostgreSQL in the native graph
// pipeline. Failed readback cannot mint a receipt; immutable mapping, replay and
// source-envelope hydration are tested. The final snapshot really activates and
// answers through the production dense/BM25 RAG path with synthetic model output.
// Graph traversal relevance and required performance are not covered by this run.
package indexing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
)

func reuseBackend(t *testing.T, ctx context.Context, repo *postgres.Repository, pin domain.SnapshotPin) (*qdrant.Store, *domain.PinnedIndex) {
	t.Helper()
	index, err := repo.LoadPinnedIndex(ctx, pin)
	if err != nil {
		t.Fatal(err)
	}
	b := index.Binding
	backend, err := qdrant.New(b.Endpoint, "", &http.Client{Timeout: 10 * time.Second}, qdrant.Binding{CorpusID: pin.CorpusID, Collection: b.Collection, Generation: b.Generation})
	if err != nil {
		t.Fatal(err)
	}
	return backend, index
}

func checkIndexReuseReadiness(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission, pin domain.SnapshotPin, publication string) {
	t.Helper()
	backend, _ := reuseBackend(t, ctx, repo, pin)
	failed := &failedIndexReuseReadback{Store: backend}
	if receipt, err := AcknowledgeIndexReuse(ctx, repo, authority, failed, pin, publication); !errors.Is(err, errIndexReuseReadback) || receipt != nil {
		t.Fatal("failed readback exposed receipt", err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM snapshot_index_reuse WHERE publication_id=$1`, publication).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed readback persisted mapping", count, err)
	}
	if _, err := db.Exec(ctx, `CREATE FUNCTION reject_index_reuse_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected reuse receipt failure'; END $$;
 CREATE TRIGGER index_reuse_fixture BEFORE INSERT ON backend_receipts FOR EACH ROW WHEN (NEW.backend=3) EXECUTE FUNCTION reject_index_reuse_fixture()`); err != nil {
		t.Fatal(err)
	}
	if receipt, err := AcknowledgeIndexReuse(ctx, repo, authority, backend, pin, publication); err == nil || receipt != nil {
		t.Fatal("receipt insert failure ignored", err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM snapshot_index_reuse WHERE publication_id=$1`, publication).Scan(&count); err != nil || count != 0 {
		t.Fatal("mapping survived receipt failure", count, err)
	}
	if _, err := db.Exec(ctx, `DROP TRIGGER index_reuse_fixture ON backend_receipts; DROP FUNCTION reject_index_reuse_fixture()`); err != nil {
		t.Fatal(err)
	}
	receipt, err := AcknowledgeIndexReuse(ctx, repo, authority, backend, pin, publication)
	if err != nil {
		t.Fatal("actual reused index acknowledgement", err)
	}
	replay, err := AcknowledgeIndexReuse(ctx, repo, authority, backend, pin, publication)
	if err != nil || !proto.Equal(receipt, replay) {
		t.Fatal("reuse replay drift", err)
	}
	if _, err = db.Exec(ctx, `UPDATE snapshot_index_reuse SET payload=payload WHERE publication_id=$1`, publication); err == nil {
		t.Fatal("reuse mapping mutable")
	}
}

func checkReusedGraphPublication(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, basePin domain.SnapshotPin, publication string, artifacts indexMemoryArtifacts, producer *pb.ProducerManifest) {
	t.Helper()
	physical, base := reuseBackend(t, ctx, repo, basePin)
	manifest, err := repo.LoadPublicationManifest(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = repo.CommitPublication(ctx, publication); err != nil {
			t.Fatal("publish graph with inherited index", err)
		}
	}
	pin, err := repo.PinActiveSnapshot(ctx, basePin.CorpusID, "read:combined:"+publication, "reader:combined", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), pin.LeaseID, pin.OwnerID)
	index, err := repo.LoadPinnedIndex(ctx, pin)
	if err != nil || !proto.Equal(index.Snapshot, manifest.SnapshotRef) || !proto.Equal(index.EvidenceSnapshot(), base.Snapshot) || index.Binding.PublicationID != base.Binding.PublicationID {
		t.Fatal("combined snapshot lost original index/source identity", err)
	}
	historical, err := repo.LoadPinnedIndex(ctx, basePin)
	if err != nil || !proto.Equal(historical.Snapshot, base.Snapshot) {
		t.Fatal("parent snapshot no longer readable", err)
	}
	page, err := repo.ReadPinnedIndexPage(ctx, pin, "")
	if err != nil || len(page) < 2 {
		t.Fatal("combined index records", err)
	}
	end, e := repo.ReadPinnedIndexPage(ctx, pin, page[len(page)-1].Record.Meta.RecordId)
	if e != nil || len(end) != 0 {
		t.Fatal("index page cursor repeated records", e)
	}
	stale := pin
	stale.ExpiresAt = stale.ExpiresAt.Add(-time.Minute)
	if _, e = repo.ReadPinnedIndexPage(ctx, stale, ""); e == nil {
		t.Fatal("expired index page lease accepted")
	}
	p := &PreparedInitialIndex{binding: index.Binding, snapshot: index.Snapshot}
	for _, item := range page {
		p.records = append(p.records, item.Record)
	}
	verifyPublishedRAG(t, ctx, repo, physical, p, artifacts, producer)
	checkIndexReadPagination(t, ctx, repo, db, pin, page)
	// A changed current index source job must be caught before publication; the
	// already published historical snapshot remains an immutable audit view.
	t.Log("combined graph/index snapshot activated; original source snapshot preserved; production RAG emitted a cited fixture draft")
}

// Isolated catalog paging fixture AFTER production query verification: these
// extra synthetic rows are not inserted into Qdrant and never get acknowledged.
// The entire private schema is dropped by the owning test afterward.
func checkIndexReadPagination(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, pin domain.SnapshotPin, original []domain.IndexCatalogRecord) {
	t.Helper()
	for i := 0; i < 65; i++ {
		r := proto.Clone(original[0].Record).(*pb.IndexRecord)
		r.Meta.RecordId = fmt.Sprintf("record:paging:%03d", i)
		identity, err := domain.IndexPointIdentity(pin.CorpusID, r.GenerationId, r.Meta.RecordId)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := proto.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, `INSERT INTO index_points(corpus_id,generation_id,record_id,point_id,identity_digest,record_payload) VALUES($1,$2,$3,$4,$5,$6)`, pin.CorpusID, r.GenerationId, r.Meta.RecordId, identity.PointID, identity.IdentityDigest, raw); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		items, err := repo.ReadPinnedIndexPage(ctx, pin, cursor)
		if err != nil {
			t.Fatal("read catalog page", err)
		}
		if len(items) == 0 {
			break
		}
		if len(items) > 64 {
			t.Fatal("unbounded page")
		}
		pages++
		for _, item := range items {
			id := item.Record.Meta.RecordId
			if seen[id] {
				t.Fatal("cursor repeated record", id)
			}
			seen[id] = true
			cursor = id
		}
	}
	if len(seen) != len(original)+65 || pages < 2 {
		t.Fatal("page coverage", len(seen), pages)
	}
	short, err := repo.PinActiveSnapshot(ctx, pin.CorpusID, "read:page-deadline:"+pin.SnapshotID, "reader:page-deadline", 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), short.LeaseID, short.OwnerID)
	lock, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `LOCK TABLE index_points IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = repo.ReadPinnedIndexPage(context.Background(), short, "")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatal("page query exceeded pin deadline", time.Since(start), err)
	}
}

var errIndexReuseReadback = errors.New("injected index readback failure")

type failedIndexReuseReadback struct{ *qdrant.Store }

func (b *failedIndexReuseReadback) VerifyPoints(context.Context, []qdrant.Point) error {
	return errIndexReuseReadback
}
