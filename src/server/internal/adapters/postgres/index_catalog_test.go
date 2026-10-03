// Exercises immutable generation/point admission against a disposable PostgreSQL,
// including replay, identity/route conflicts, atomic rollback and stale fences.
// Requires REGULAGRAPH_TEST_POSTGRES_DSN; skipping is not a PASS for storage gates.
// Synthetic C01 vectors verify consistency, not model or legal quality.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func indexCatalogFixture(t *testing.T) (*pb.IndexGeneration, []*pb.IndexRecord) {
	t.Helper()
	raw, err := os.ReadFile("../../../../../tests/fixtures/wire-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name  string
			Value json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		if c.Name == "index-build-batch-valid" {
			b := new(pb.IndexBatch)
			if err = protojson.Unmarshal(c.Value, b); err != nil {
				t.Fatal(err)
			}
			return b.Generation, b.Records
		}
	}
	t.Fatal("missing shared INDEX fixture")
	return nil, nil
}

func TestIndexCatalogAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	repo, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	path, err := filepath.Abs("../../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(path)); err != nil {
		t.Fatal(err)
	}
	corpus := fmt.Sprintf("corpus:index-catalog:%d", time.Now().UnixNano())
	pub := "publication:" + corpus
	reservation, err := repo.ReservePublication(ctx, pub, "", corpus, "snapshot:"+corpus, "")
	if err != nil {
		t.Fatal(err)
	}
	generation, records := indexCatalogFixture(t)
	generation.Meta.CorpusId = corpus
	b := domain.IndexCatalogBinding{PublicationID: pub, Fence: reservation.Fence, Endpoint: "http://127.0.0.1:56341", Collection: fmt.Sprintf("catalog_%d", time.Now().UnixNano()), Generation: generation}
	for _, record := range records {
		record.Meta.CorpusId = corpus
		record.Meta.Visibility.FromSeq = reservation.Sequence
		record.FilterMetadata.Visibility.FromSeq = reservation.Sequence
	}
	for range 2 {
		if err = repo.RegisterIndexGeneration(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.LoadIndexGeneration(ctx, corpus, generation.Meta.RecordId)
	if err != nil || !equalIndexBinding(b, got) {
		t.Fatal("binding replay", got, err)
	}
	wrong := b
	wrong.Endpoint = "http://127.0.0.1:1"
	if repo.RegisterIndexGeneration(ctx, wrong) == nil {
		t.Fatal("endpoint drift accepted")
	}
	wrong = b
	wrong.Fence++
	if repo.RegisterIndexGeneration(ctx, wrong) == nil {
		t.Fatal("stale fence accepted")
	}
	for _, mutate := range []func(*pb.IndexRecord){
		func(r *pb.IndexRecord) { r.DenseVector.Values = r.DenseVector.Values[:1] },
		func(r *pb.IndexRecord) { r.DenseVector.Values = []float32{0, 0} },
		func(r *pb.IndexRecord) {
			r.SparseVector = &pb.SparseVector{Indices: []uint32{2, 1}, Values: []float32{1, 1}}
		},
		func(r *pb.IndexRecord) { r.SparseVector.Values[0] = -1 },
	} {
		invalid := proto.Clone(records[0]).(*pb.IndexRecord)
		mutate(invalid)
		if _, err = repo.ReserveIndexPoints(ctx, b, []*pb.IndexRecord{invalid}); err == nil {
			t.Fatal("invalid vector poisoned immutable catalog")
		}
	}
	ids, err := repo.ReserveIndexPoints(ctx, b, records)
	if err != nil || len(ids) != len(records) {
		t.Fatal(ids, err)
	}
	again, err := repo.ReserveIndexPoints(ctx, b, records)
	if err != nil || fmt.Sprint(ids) != fmt.Sprint(again) {
		t.Fatal("point replay", again, err)
	}
	changed := proto.Clone(records[0]).(*pb.IndexRecord)
	changed.ChunkId = "chunk:changed"
	if _, err = repo.ReserveIndexPoints(ctx, b, []*pb.IndexRecord{changed}); err == nil {
		t.Fatal("record overwrite accepted")
	}
	newRecord := proto.Clone(records[0]).(*pb.IndexRecord)
	newRecord.Meta.RecordId = "index:new"
	if _, err = repo.ReserveIndexPoints(ctx, b, []*pb.IndexRecord{newRecord, changed}); err == nil {
		t.Fatal("mixed invalid batch accepted")
	}
	var count int
	if err = repo.pool.QueryRow(ctx, `SELECT count(*) FROM index_points WHERE corpus_id=$1`, corpus).Scan(&count); err != nil || count != len(records) {
		t.Fatal("partial allocation escaped rollback", count, err)
	}
	// Inject a different owner of the future UUID, demonstrating that a truncated
	// hash collision is detected rather than treated as another record's point.
	future, _ := domain.IndexPointIdentity(corpus, generation.Meta.RecordId, newRecord.Meta.RecordId)
	raw, _ := proto.Marshal(records[0])
	_, err = repo.pool.Exec(ctx, `INSERT INTO index_points(corpus_id,generation_id,record_id,point_id,identity_digest,record_payload) VALUES($1,$2,'collision:owner',$3::uuid,$4,$5)`, corpus, generation.Meta.RecordId, future.PointID, future.IdentityDigest, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveIndexPoints(ctx, b, []*pb.IndexRecord{newRecord}); err == nil {
		t.Fatal("UUID collision accepted")
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE index_generations SET endpoint='http://changed' WHERE corpus_id=$1`, corpus); err == nil {
		t.Fatal("generation trigger permitted mutation")
	}
	if _, err = repo.pool.Exec(ctx, `DELETE FROM index_points WHERE corpus_id=$1`, corpus); err == nil {
		t.Fatal("point trigger permitted deletion")
	}
	if err = repo.AbortPublication(ctx, pub); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveIndexPoints(ctx, b, records); err == nil {
		t.Fatal("aborted publication admitted points")
	}
}
