// Verifies exact graph record bytes, projected properties, directed edges and
// operation coverage before sealing a generation against further mutations.
// Reads and seal share the same Neo4j lock as writes. Proof describes this direct
// backend only; PostgreSQL publication and serving authorization remain callers'
// responsibility. Verification accepts at most 64MiB input/256 deltas; bounded
// count scans and database-side property comparisons return only scalars over
// Bolt, including for duplicate/shared records. Measure readback/lock/RSS separately
// from acknowledged writes; required benchmark acceptance remains unmeasured.
package neo4j

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"sort"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type ReadyProof struct {
	Generation, BindingHash, OperationsHash string
	Records, Edges, Operations              uint64
}

func (s *Store) VerifyAndSeal(ctx context.Context, deltas []*pb.GraphDelta) (ReadyProof, error) {
	var proof ReadyProof
	if !s.schemaReady.Load() {
		return proof, ErrGraphConflict
	}
	if len(deltas) == 0 || len(deltas) > 256 {
		return proof, errors.New("bounded nonempty graph inventory required")
	}
	remaining := 64 << 20
	nodes := map[string]record{}
	edges := map[edge]bool{}
	operations := map[string]operation{}
	for _, delta := range deltas {
		size := proto.Size(delta)
		if size > remaining {
			return proof, errors.New("graph inventory byte budget exceeded")
		}
		remaining -= size
		p, err := s.project(delta)
		if err != nil {
			return proof, err
		}
		if _, ok := operations[p.id]; ok {
			return proof, ErrGraphConflict
		}
		operations[p.id] = operation{p.hash, p.bytes}
		for _, n := range p.records {
			if old, ok := nodes[n.id]; ok && !reflect.DeepEqual(old.props, n.props) {
				return proof, ErrGraphConflict
			}
			nodes[n.id] = n
		}
		for _, e := range p.edges {
			edges[e] = true
		}
	}
	ids := make([]string, 0, len(operations))
	for id := range operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := sha256.New()
	h.Write([]byte("graph-operations-v1"))
	for _, id := range ids {
		fmt.Fprintf(h, "%d:%s%s", len(id), id, operations[id].hash)
	}
	proof = ReadyProof{s.binding.Generation, s.bindingHash, fmt.Sprintf("%x", h.Sum(nil)), uint64(len(nodes)), uint64(len(edges)), uint64(len(operations))}
	err := s.transaction(ctx, true, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
		if _, err := s.lockGeneration(ctx, tx); err != nil {
			return err
		}
		if err := s.verifyRecords(ctx, tx, nodes, edges, operations); err != nil {
			return err
		}
		params := s.params()
		params["digest"] = proof.OperationsHash
		_, err := one(ctx, tx, `MATCH (g:RGGeneration {corpus:$corpus,generation:$generation}) SET g.state='SEALED',g.operations_hash=$digest RETURN g.state`, params)
		return err
	})
	if err != nil {
		return ReadyProof{}, err
	}
	return proof, nil
}

// operation records include serialized input size so the intake budget is replay-safe.
type operation struct {
	hash  string
	bytes int64
}

func (s *Store) verifyRecords(ctx context.Context, tx bolt.ExplicitTransaction, nodes map[string]record, edges map[edge]bool, operations map[string]operation) error {
	params := s.params()
	// Only counts/booleans cross Bolt. Compare properties in the database so a
	// corrupted backend value cannot force an unbounded client payload allocation.
	count := func(query string, expected int) error {
		params["limit"] = int64(expected + 1)
		row, err := one(ctx, tx, query, params)
		if err != nil {
			return err
		}
		if row.Values[0] != int64(expected) {
			return ErrGraphConflict
		}
		return nil
	}
	if err := count(`MATCH (n:RGRecord {corpus:$corpus,generation:$generation}) WITH n LIMIT $limit RETURN count(n)`, len(nodes)); err != nil {
		return err
	}
	rows := make([]any, 0, len(nodes))
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rows = append(rows, nodes[id].props)
	}
	for start := 0; start < len(rows); start += pageSize {
		page := rows[start:min(start+pageSize, len(rows))]
		params["rows"] = page
		row, err := one(ctx, tx, `UNWIND $rows AS row OPTIONAL MATCH (n:RGRecord {corpus:$corpus,generation:$generation,id:row.id})
RETURN count(*),sum(CASE WHEN properties(n)=row THEN 1 ELSE 0 END)`, params)
		if err != nil {
			return err
		}
		if row.Values[0] != int64(len(page)) || row.Values[1] != int64(len(page)) {
			return ErrGraphConflict
		}
	}
	if err := count(`MATCH (a:RGRecord {corpus:$corpus,generation:$generation})-[r]->() WITH r LIMIT $limit RETURN count(r)`, len(edges)); err != nil {
		return err
	}
	ordered := make([]edge, 0, len(edges))
	for e := range edges {
		ordered = append(ordered, e)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if a.from != b.from {
			return a.from < b.from
		}
		return a.to < b.to
	})
	for start := 0; start < len(ordered); {
		end := start + 1
		for end < len(ordered) && end-start < pageSize && ordered[end].kind == ordered[start].kind {
			end++
		}
		page := make([]any, 0, end-start)
		for _, e := range ordered[start:end] {
			page = append(page, map[string]any{"from": e.from, "to": e.to, "from_kind": e.fromKind, "to_kind": e.toKind})
		}
		params["rows"] = page
		row, err := one(ctx, tx, `UNWIND $rows AS row OPTIONAL MATCH (a:RGRecord {corpus:$corpus,generation:$generation,id:row.from})-[r:`+ordered[start].kind+`]->(b:RGRecord {corpus:$corpus,generation:$generation,id:row.to})
WITH row,count(r) AS total,sum(CASE WHEN properties(r)={from_seq:$sequence} AND a.kind=row.from_kind AND b.kind=row.to_kind THEN 1 ELSE 0 END) AS valid
RETURN count(*),sum(CASE WHEN total=1 AND valid=1 THEN 1 ELSE 0 END)`, params)
		if err != nil {
			return err
		}
		if row.Values[0] != int64(len(page)) || row.Values[1] != int64(len(page)) {
			return ErrGraphConflict
		}
		start = end
	}
	row, err := one(ctx, tx, `MATCH (a:RGRecord {corpus:$corpus,generation:$generation})<-[r]-(b)
WHERE NOT b:RGRecord OR b.corpus IS NULL OR b.generation IS NULL OR b.corpus<>$corpus OR b.generation<>$generation
WITH r LIMIT 1 RETURN count(r)`, params)
	if err != nil {
		return err
	}
	if row.Values[0] != int64(0) {
		return ErrGraphConflict
	}
	if err = count(`MATCH (o:RGOperation {corpus:$corpus,generation:$generation}) WITH o LIMIT $limit RETURN count(o)`, len(operations)); err != nil {
		return err
	}
	var totalBytes int64
	rows = rows[:0]
	for id, o := range operations {
		totalBytes += o.bytes
		rows = append(rows, map[string]any{"corpus": s.binding.CorpusID, "generation": s.binding.Generation, "id": id, "hash": o.hash, "bytes": o.bytes})
	}
	params["rows"] = rows
	row, err = one(ctx, tx, `UNWIND $rows AS row OPTIONAL MATCH (o:RGOperation {corpus:$corpus,generation:$generation,id:row.id}) RETURN count(*),sum(CASE WHEN properties(o)=row THEN 1 ELSE 0 END)`, params)
	if err != nil {
		return err
	}
	if row.Values[0] != int64(len(rows)) || row.Values[1] != int64(len(rows)) {
		return ErrGraphConflict
	}
	row, err = one(ctx, tx, `MATCH (g:RGGeneration {corpus:$corpus,generation:$generation}) RETURN g.operations,g.bytes`, params)
	if err != nil {
		return err
	}
	if row.Values[0] != int64(len(operations)) || row.Values[1] != totalBytes {
		return ErrGraphConflict
	}
	return nil
}
