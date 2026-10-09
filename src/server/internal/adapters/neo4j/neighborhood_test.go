// Verifies physical adjacency selection and production traversal on disposable
// Neo4j records. Corruption must fail before paths are exposed; independent graph
// quality/recall and source applicability remain outside these backend fixtures.
package neo4j

import (
	"context"
	"testing"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"regulagraph.local/server/internal/domain"
	graphretrieval "regulagraph.local/server/internal/retrieval/graph"
)

func checkNeighborhood(t *testing.T, ctx context.Context, s *Store, reader *Reader) {
	t.Helper()
	limits := domain.GraphReadLimits{Assertions: 10, Supports: 10, Bytes: 1 << 20}
	for _, seed := range []string{"entity:a", "entity:b"} {
		got, err := reader.ReadNeighborhood(ctx, []string{seed}, limits)
		if err != nil || len(got.Assertions) != 1 || len(got.Supports) != 2 || len(got.Entities) != 2 || got.MoreAssertions {
			t.Fatal("physical neighborhood", seed, got, err)
		}
		paths, err := graphretrieval.Traverse(ctx, reader, reader.snapshot, []string{seed}, graphretrieval.TraversalConfig{MaximumHops: 2, MaximumPaths: 10, Read: limits})
		if err != nil || len(paths.Paths) != 1 || !paths.FrontierExhausted {
			t.Fatal("backend traversal", seed, paths, err)
		}
	}
	for _, mutation := range []string{
		`MATCH(a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'})-[r:RG_OBJECT]->() SET r.from_seq=999`,
		`MATCH(a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'}),(b:RGRecord {corpus:$corpus,generation:$generation,id:'entity:b'}) CREATE(a)-[:RG_SUBJECT {from_seq:$sequence}]->(b)`,
		`MATCH(a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'})-[r:RG_OBJECT]->(),(b:RGRecord {corpus:$corpus,generation:$generation,id:'entity:a'}) DELETE r CREATE(a)-[:RG_SUBJECT {from_seq:$sequence}]->(b)`,
		`MATCH(a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'})-[r:RG_SUBJECT|RG_OBJECT]->() DELETE r`,
		`MATCH(s:RGRecord {corpus:$corpus,generation:$generation,id:'support:first'})-[r:RG_SUPPORT]->() DELETE r`,
	} {
		if err := s.transaction(ctx, true, func(c context.Context, tx bolt.ExplicitTransaction) error {
			rows, e := tx.Run(c, mutation, s.params())
			if e != nil {
				return e
			}
			_, e = rows.Consume(c)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if got, err := reader.ReadNeighborhood(ctx, []string{"entity:a"}, limits); err == nil || got != nil {
			t.Fatal("corrupt adjacency admitted", mutation)
		}
		if err := s.transaction(ctx, true, func(c context.Context, tx bolt.ExplicitTransaction) error {
			_, e := one(c, tx, `MATCH(a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'})-[r:RG_SUBJECT|RG_OBJECT]->() DELETE r RETURN count(*)`, s.params())
			if e != nil {
				return e
			}
			_, e = one(c, tx, `MATCH(a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'}),(s:RGRecord {corpus:$corpus,generation:$generation,id:'entity:a'}),(o:RGRecord {corpus:$corpus,generation:$generation,id:'entity:b'}) CREATE(a)-[:RG_SUBJECT {from_seq:$sequence}]->(s),(a)-[:RG_OBJECT {from_seq:$sequence}]->(o) RETURN count(*)`, s.params())
			if e != nil {
				return e
			}
			_, e = one(c, tx, `MATCH(s:RGRecord {corpus:$corpus,generation:$generation,kind:'support'}),(a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'}) WHERE s.assertion=a.id MERGE(s)-[r:RG_SUPPORT]->(a) SET r.from_seq=$sequence RETURN count(*)`, s.params())
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ReadNeighborhood(ctx, []string{"entity:a"}, limits); err != nil {
			t.Fatal("restore adjacency", err)
		}
	}
	limits.Supports = 1
	if got, err := reader.ReadNeighborhood(ctx, []string{"entity:a"}, limits); err != domain.ErrGraphReadBudget || got != nil {
		t.Fatal("support overflow silently discarded alternate support", got, err)
	}
	limits.Supports = 10
	limits.Bytes = 1
	partial, err := graphretrieval.Traverse(ctx, reader, reader.snapshot, []string{"entity:a"}, graphretrieval.TraversalConfig{MaximumHops: 2, MaximumPaths: 10, Read: limits})
	if err != nil || len(partial.StopReasons) != 1 || partial.StopReasons[0] != "read_budget" || partial.FrontierExhausted {
		t.Fatal("byte overflow must be explicit partial", partial, err)
	}
}
