// Applies one admitted additive delta atomically under the Neo4j generation
// lock. Operation ID+hash gives deterministic replay, and node payload conflicts
// roll back the entire transaction. UNWIND pages bound Bolt messages; endpoints
// are checked before adjacency is acknowledged. No transparent driver retries,
// PostgreSQL receipt, publication activation or destructive cleanup occurs here.
// Measure acknowledged commit/lock/queue latency and peak RSS against required
// GRAPH/STORAGE benchmark targets; synthetic integration is not acceptance.
package neo4j

import (
	"context"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const pageSize = 256

func (s *Store) ApplyGraphDelta(ctx context.Context, delta *pb.GraphDelta) error {
	if !s.schemaReady.Load() {
		return ErrGraphConflict
	}
	p, err := s.project(delta)
	if err != nil {
		return err
	}
	return s.transaction(ctx, true, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
		sealed, err := s.lockGeneration(ctx, tx)
		if err != nil {
			return err
		}
		params := s.params()
		params["id"], params["hash"] = p.id, p.hash
		r, err := one(ctx, tx, `OPTIONAL MATCH (o:RGOperation {corpus:$corpus,generation:$generation,id:$id}) RETURN o.hash`, params)
		if err != nil {
			return err
		}
		if r.Values[0] != nil {
			if r.Values[0] != p.hash {
				return ErrGraphConflict
			}
			return nil
		}
		if sealed {
			return ErrGraphConflict
		}
		budget, err := one(ctx, tx, `MATCH (g:RGGeneration {corpus:$corpus,generation:$generation}) RETURN g.operations,g.bytes`, params)
		if err != nil {
			return err
		}
		operations, countOK := budget.Values[0].(int64)
		used, bytesOK := budget.Values[1].(int64)
		if !countOK || !bytesOK || operations < 0 || operations >= 256 || used < 0 || used > (64<<20)-p.bytes {
			return ErrGraphConflict
		}
		for start := 0; start < len(p.records); start += pageSize {
			end := min(start+pageSize, len(p.records))
			rows := make([]any, 0, end-start)
			for _, v := range p.records[start:end] {
				rows = append(rows, v.props)
			}
			params["rows"] = rows
			r, err = one(ctx, tx, `UNWIND $rows AS row
MERGE (n:RGRecord {corpus:$corpus,generation:$generation,id:row.id})
ON CREATE SET n += row
RETURN count(n) AS total,sum(CASE WHEN n.hash=row.hash AND n.kind=row.kind AND n.from_seq=$sequence AND n.to_seq IS NULL THEN 0 ELSE 1 END) AS conflicts`, params)
			if err != nil {
				return err
			}
			if r.Values[0] != int64(len(rows)) || r.Values[1] != int64(0) {
				return ErrGraphConflict
			}
		}
		for start := 0; start < len(p.edges); {
			end := start + 1
			for end < len(p.edges) && end-start < pageSize && p.edges[end].kind == p.edges[start].kind {
				end++
			}
			rows := make([]any, 0, end-start)
			for _, e := range p.edges[start:end] {
				rows = append(rows, map[string]any{"from": e.from, "to": e.to, "from_kind": e.fromKind, "to_kind": e.toKind})
			}
			params["rows"] = rows
			// Relationship type comes only from the closed projection vocabulary.
			query := `UNWIND $rows AS row MATCH (a:RGRecord {corpus:$corpus,generation:$generation,id:row.from}),(b:RGRecord {corpus:$corpus,generation:$generation,id:row.to})
WHERE a.kind=row.from_kind AND b.kind=row.to_kind AND a.from_seq=$sequence AND b.from_seq=$sequence AND a.to_seq IS NULL AND b.to_seq IS NULL
MERGE (a)-[r:` + p.edges[start].kind + `]->(b) ON CREATE SET r.from_seq=$sequence
RETURN count(r),sum(CASE WHEN r.from_seq=$sequence AND r.to_seq IS NULL THEN 0 ELSE 1 END)`
			r, err = one(ctx, tx, query, params)
			if err != nil {
				return err
			}
			if r.Values[0] != int64(len(rows)) || r.Values[1] != int64(0) {
				return ErrGraphConflict
			}
			start = end
		}
		params["bytes"] = p.bytes
		_, err = one(ctx, tx, `MATCH (g:RGGeneration {corpus:$corpus,generation:$generation}) SET g.operations=g.operations+1,g.bytes=g.bytes+$bytes
CREATE (o:RGOperation {corpus:$corpus,generation:$generation,id:$id,hash:$hash,bytes:$bytes}) RETURN o.id`, params)
		return err
	})
}
