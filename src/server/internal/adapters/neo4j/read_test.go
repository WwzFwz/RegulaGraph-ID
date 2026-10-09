// Checks real Neo4j read-only admission, typed batch ordering and corruption
// rejection under size/deadline limits. PostgreSQL admission is synthesized here;
// the native integration separately obtains it from a published snapshot. All
// graph data belongs to a unique disposable corpus and is removed after the test.
// These fixtures prove integrity boundaries, not traversal quality or performance.
package neo4j

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestSealedGraphReaderAgainstNeo4j(t *testing.T) {
	uri := os.Getenv("REGULAGRAPH_TEST_NEO4J_URI")
	if uri == "" {
		t.Skip("disposable Neo4j required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, delta := graphFixture(fmt.Sprintf("corpus:graph-read:%d", time.Now().UnixNano()))
	config := Config{URI: uri, Username: "neo4j", Password: os.Getenv("REGULAGRAPH_TEST_NEO4J_PASSWORD"), Database: "neo4j", PoolSize: 2, Timeout: 5 * time.Second}
	s, err := New(config, b)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.transaction(ctx, false, func(c context.Context, tx bolt.ExplicitTransaction) error {
		row, e := one(c, tx, `RETURN valueType($bytes),valueType($integers),valueType($strings)`, map[string]any{"bytes": []byte{1, 2}, "integers": []int64{1, 2}, "strings": []string{"a", "b"}})
		if e == nil && (row.Values[0] != "LIST<INTEGER NOT NULL> NOT NULL" || row.Values[1] != row.Values[0] || row.Values[2] != "LIST<STRING NOT NULL> NOT NULL") {
			return fmt.Errorf("unexpected Neo4j property types: %v", row.Values)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if e := s.transaction(cleanup, true, func(c context.Context, tx bolt.ExplicitTransaction) error {
			rows, e := tx.Run(c, `MATCH(n {corpus:$corpus}) DETACH DELETE n`, map[string]any{"corpus": b.CorpusID})
			if e != nil {
				return e
			}
			_, e = rows.Consume(c)
			return e
		}); e != nil {
			t.Error(e)
		}
	}()
	proof, err := s.Describe([]*pb.GraphDelta{delta})
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	view := &domain.PinnedGraph{AuthScope: "scope:fixture", Pin: domain.SnapshotPin{CorpusID: b.CorpusID, SnapshotID: "snapshot:graph", Sequence: b.Sequence, LeaseID: "lease:fixture", OwnerID: "reader:fixture", ExpiresAt: time.Now().Add(time.Minute)},
		Snapshot: &pb.SnapshotRef{CorpusId: b.CorpusID, SnapshotId: "snapshot:graph", Sequence: b.Sequence, ManifestHash: &pb.ContentHash{Sha256: hash}, RepresentationGeneration: "index:fixture"},
		Catalog: domain.GraphCatalogBinding{Binding: b, Endpoint: uri, Database: "neo4j", BindingHash: proof.BindingHash, OperationsHash: proof.OperationsHash, InventoryHash: hash, Records: proof.Records, Edges: proof.Edges, Operations: proof.Operations,
			Outputs: []*pb.ArtifactRef{{ArtifactId: "output:fixture", ContentHash: &pb.ContentHash{Sha256: hash}, StorageKey: "fixture/graph.bin", MediaType: domain.GraphDeltaMediaType, ByteSize: 1, SchemaVersion: 1}}}}
	if _, err = s.OpenReader(ctx, view); err == nil {
		t.Fatal("absent generation admitted")
	}
	if err = s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyGraphDelta(ctx, delta); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OpenReader(ctx, view); err == nil {
		t.Fatal("unsealed generation admitted")
	}
	if _, err = s.VerifyAndSeal(ctx, []*pb.GraphDelta{delta}); err != nil {
		t.Fatal(err)
	}
	// Cold read store never bootstraps constraints or marks schemaReady.
	cold, err := New(config, b)
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close(context.Background())
	reader, err := cold.OpenReader(ctx, view)
	if err != nil || cold.schemaReady.Load() {
		t.Fatal("read admission mutated schema", err)
	}
	entities, err := reader.ReadRecords(ctx, "entity", []string{delta.Entities[1].Meta.RecordId, delta.Entities[0].Meta.RecordId}, 1<<20)
	if err != nil || len(entities) != 2 || !proto.Equal(entities[0], delta.Entities[1]) || !proto.Equal(entities[1], delta.Entities[0]) {
		t.Fatal("typed entity order", err)
	}
	for _, selection := range []struct {
		kind     string
		id       string
		expected proto.Message
	}{{"assertion", delta.Assertions[0].Meta.RecordId, delta.Assertions[0]}, {"support", delta.Supports[0].Meta.RecordId, delta.Supports[0]}} {
		values, e := reader.ReadRecords(ctx, selection.kind, []string{selection.id}, 1<<20)
		if e != nil || len(values) != 1 || !proto.Equal(values[0], selection.expected) {
			t.Fatal("typed graph read", selection.kind, e)
		}
	}
	for _, bad := range []struct {
		kind  string
		ids   []string
		bytes uint64
	}{{"entity", []string{"entity:missing"}, 1024}, {"support", []string{delta.Entities[0].Meta.RecordId}, 1024}, {"unknown", []string{"entity:a"}, 1024}, {"entity", []string{"entity:a", "entity:a"}, 1024}, {"entity", []string{"entity:a"}, 1}} {
		if values, e := reader.ReadRecords(ctx, bad.kind, bad.ids, bad.bytes); e == nil || values != nil {
			t.Fatal("bad graph selection returned partial data", bad, e)
		}
	}
	projection, err := s.project(delta)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"SET n.label='corrupt'", "SET n.payload=$large", "SET n.hash='invalid'", "SET n.from_seq=999", "SET n.hash=$strings", "SET n.payload=$strings", "SET n.payload=$integers"} {
		if err = s.transaction(ctx, true, func(c context.Context, tx bolt.ExplicitTransaction) error {
			p := s.params()
			p["id"] = "entity:a"
			p["large"] = make([]byte, 1<<20)
			strings64 := make([]string, 64)
			for i := range strings64 {
				strings64[i] = strings.Repeat("x", 1<<14)
			}
			p["strings"], p["integers"] = strings64, []int64{1 << 62, 1 << 61}
			_, e := one(c, tx, `MATCH(n:RGRecord {corpus:$corpus,generation:$generation,id:$id}) `+mutation+` RETURN count(n)`, p)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if values, e := reader.ReadRecords(ctx, "entity", []string{"entity:a"}, 1024); e == nil || values != nil {
			t.Fatal("corrupt graph record accepted", mutation, e)
		}
		// Check DB-side results directly: rejecting the Go type after transferring
		// a huge list is insufficient. Also simulate a between-statements swap by
		// supplying the corrupted list's length to the second-stage query.
		if mutation == "SET n.hash=$strings" || mutation == "SET n.payload=$strings" {
			if err = s.transaction(ctx, false, func(c context.Context, tx bolt.ExplicitTransaction) error {
				p := s.params()
				p["ids"], p["kind"] = []string{"entity:a"}, "entity"
				row, e := one(c, tx, graphReadMetadata, p)
				if e != nil {
					return e
				}
				column := 2
				if mutation == "SET n.payload=$strings" {
					column = 3
				}
				if row.Values[column] != nil {
					return fmt.Errorf("metadata returned substituted property: column %d", column)
				}
				p["selection"] = []any{map[string]any{"id": "entity:a", "hash": projection.records[0].hash, "bytes": int64(64)}}
				row, e = one(c, tx, graphReadPayload, p)
				if e == nil && row.Values[1] != nil {
					return fmt.Errorf("payload query transferred substituted property")
				}
				return e
			}); err != nil {
				t.Fatal(mutation, err)
			}
		}
		if err = s.transaction(ctx, true, func(c context.Context, tx bolt.ExplicitTransaction) error {
			p := s.params()
			p["props"] = projection.records[0].props
			_, e := one(c, tx, `MATCH(n:RGRecord {corpus:$corpus,generation:$generation,id:$props.id}) SET n=$props RETURN count(n)`, p)
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}
	reader.expires = time.Now().Add(-time.Second)
	if values, e := reader.ReadRecords(context.Background(), "entity", []string{"entity:a"}, 1024); e == nil || values != nil {
		t.Fatal("expired reader returned data", e)
	}
}
