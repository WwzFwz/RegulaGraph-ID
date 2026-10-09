// Expands entity seeds through typed assertion adjacency in a sealed generation.
// IDs are selected in bounded batches, then decoded through the authenticated
// record reader. Both directions are discoverable without reversing predicates.
// All supports of selected assertions are required; overflow is explicit. Physical
// selected adjacency must exactly match protobuf endpoints/qualifiers/exceptions.
// Legal-time/source-text acceptance belongs to evidence hydration, not this adapter.
// Measure frontier fan-out, bytes, round trips and p95/p99 under required GRAPH and
// RETRIEVAL gates; limits control resources and do not prove recall or completeness.
package neo4j

import (
	"context"
	"errors"
	"sort"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Reader) ReadNeighborhood(ctx context.Context, seeds []string, limits domain.GraphReadLimits) (*domain.GraphNeighborhood, error) {
	if r == nil || r.store == nil || ctx == nil || len(seeds) < 1 || len(seeds) > 64 || limits.Assertions < 1 || limits.Assertions > 128 || limits.Supports < 1 || limits.Supports > 256 || limits.Bytes < 1 || limits.Bytes > 16<<20 {
		return nil, errors.New("bounded graph frontier and read limits required")
	}
	ctx, cancel := context.WithDeadline(ctx, r.expires)
	defer cancel()
	out := &domain.GraphNeighborhood{Snapshot: proto.Clone(r.snapshot).(*pb.SnapshotRef)}
	read := func(kind string, ids []string) ([]proto.Message, error) {
		if len(ids) == 0 {
			return nil, nil
		}
		if out.ReadBytes >= limits.Bytes {
			return nil, domain.ErrGraphReadBudget
		}
		var transferred uint64
		values, err := r.readRecords(ctx, kind, ids, limits.Bytes-out.ReadBytes, &transferred)
		if err != nil {
			return nil, err
		}
		out.ReadBytes += transferred
		return values, nil
	}
	seedRecords, err := read("entity", seeds)
	if err != nil {
		return nil, err
	}
	entityIDs := map[string]bool{}
	for _, v := range seedRecords {
		entity := v.(*pb.CanonicalEntity)
		out.Entities = append(out.Entities, entity)
		entityIDs[entity.Meta.RecordId] = true
	}
	ids, more, err := r.selectNeighbors(ctx, seeds, limits.Assertions, false)
	if err != nil {
		return nil, err
	}
	out.MoreAssertions = more
	assertions, err := read("assertion", ids)
	if err != nil {
		return nil, err
	}
	missingEntities := map[string]bool{}
	for _, v := range assertions {
		a := v.(*pb.RelationAssertion)
		if !entityIDs[a.SubjectId] && !entityIDs[a.ObjectId] {
			return nil, ErrGraphConflict
		}
		out.Assertions = append(out.Assertions, a)
		for _, id := range []string{a.SubjectId, a.ObjectId} {
			if !entityIDs[id] {
				missingEntities[id] = true
			}
		}
	}
	if len(ids) != 0 {
		supportIDs, overflow, err := r.selectNeighbors(ctx, ids, limits.Supports, true)
		if err != nil {
			return nil, err
		}
		if overflow {
			return nil, domain.ErrGraphReadBudget
		}
		supports, err := read("support", supportIDs)
		if err != nil {
			return nil, err
		}
		selected := map[string]bool{}
		for _, id := range ids {
			selected[id] = true
		}
		supported := map[string]bool{}
		for _, v := range supports {
			s := v.(*pb.SupportRecord)
			if !selected[s.AssertionId] {
				return nil, ErrGraphConflict
			}
			supported[s.AssertionId] = true
			out.Supports = append(out.Supports, s)
		}
		if len(supported) != len(ids) {
			return nil, ErrGraphConflict
		}
	}
	missing := make([]string, 0, len(missingEntities))
	for id := range missingEntities {
		missing = append(missing, id)
	}
	sort.Strings(missing)
	entities, err := read("entity", missing)
	if err != nil {
		return nil, err
	}
	for _, v := range entities {
		out.Entities = append(out.Entities, v.(*pb.CanonicalEntity))
	}
	if err = r.verifyNeighborhoodEdges(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Selection returns only bounded scalar IDs. Matching does not hide an invalid
// adjacent node by filtering it away: corrupt scope/type/ID becomes an error.
func (r *Reader) selectNeighbors(ctx context.Context, ids []string, limit int, supports bool) ([]string, bool, error) {
	pattern := `<-[:RG_SUBJECT|RG_OBJECT]-(n)`
	kind := "assertion"
	projection := `UNWIND $ids AS id MATCH(n:RGRecord {corpus:$corpus,generation:$generation,subject:id}) RETURN n
 UNION UNWIND $ids AS id MATCH(n:RGRecord {corpus:$corpus,generation:$generation,object:id}) RETURN n`
	if supports {
		pattern, kind = `<-[:RG_SUPPORT]-(n)`, "support"
		projection = `UNWIND $ids AS id MATCH(n:RGRecord {corpus:$corpus,generation:$generation,assertion:id}) RETURN n`
	}
	var out []string
	err := r.store.transaction(ctx, false, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
		if err := r.verifySeal(ctx, tx); err != nil {
			return err
		}
		p := r.store.params()
		p["ids"], p["kind"], p["limit"] = ids, kind, int64(limit+1)
		query := `CALL { UNWIND $ids AS id MATCH (e:RGRecord {corpus:$corpus,generation:$generation,id:id})` + pattern + ` RETURN n UNION ` + projection + ` }
 WITH DISTINCT n ORDER BY n.id LIMIT $limit
 RETURN CASE WHEN valueType(n.id)='STRING NOT NULL' THEN CASE WHEN size(n.id)<=256 AND n.id =~ '^[!-~]+$' THEN n.id END END,
 n:RGRecord AND n.corpus=$corpus AND n.generation=$generation AND n.kind=$kind AND n.from_seq=$sequence AND n.to_seq IS NULL`
		rows, err := tx.Run(ctx, query, p)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for rows.Next(ctx) {
			row := rows.Record()
			id, ok := row.Values[0].(string)
			if !ok || row.Values[1] != true || seen[id] || len(out) > limit {
				return ErrGraphConflict
			}
			seen[id] = true
			out = append(out, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

func (r *Reader) verifyNeighborhoodEdges(ctx context.Context, neighborhood *domain.GraphNeighborhood) error {
	var expected []any
	for _, a := range neighborhood.Assertions {
		edges := []map[string]any{{"kind": "RG_SUBJECT", "id": a.SubjectId, "target_kind": "entity"}, {"kind": "RG_OBJECT", "id": a.ObjectId, "target_kind": "entity"}}
		seen := map[string]bool{}
		for _, id := range a.ExceptionRefs {
			if !seen["exception:"+id] {
				edges = append(edges, map[string]any{"kind": "RG_EXCEPTION", "id": id, "target_kind": "assertion"})
				seen["exception:"+id] = true
			}
		}
		for _, q := range a.Qualifiers {
			if id := q.GetCanonicalId(); id != "" && !seen["qualifier:"+id] {
				edges = append(edges, map[string]any{"kind": "RG_QUALIFIER", "id": id, "target_kind": "entity"})
				seen["qualifier:"+id] = true
			}
		}
		expected = append(expected, map[string]any{"id": a.Meta.RecordId, "edges": edges})
	}
	for _, s := range neighborhood.Supports {
		expected = append(expected, map[string]any{"id": s.Meta.RecordId, "edges": []map[string]any{{"kind": "RG_SUPPORT", "id": s.AssertionId, "target_kind": "assertion"}}})
	}
	return r.store.transaction(ctx, false, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
		if len(expected) != 0 {
			p := r.store.params()
			p["expected"] = expected
			row, err := one(ctx, tx, `UNWIND $expected AS item MATCH(n:RGRecord {corpus:$corpus,generation:$generation,id:item.id})
 OPTIONAL MATCH(n)-[edge]->(target)
 WITH item,count(edge) AS total,count(DISTINCT [type(edge),target.id]) AS distinct_edges,sum(CASE WHEN target:RGRecord AND target.corpus=$corpus AND target.generation=$generation
 AND target.from_seq=$sequence AND target.to_seq IS NULL AND properties(edge)={from_seq:$sequence}
 AND any(e IN item.edges WHERE type(edge)=e.kind AND target.id=e.id AND target.kind=e.target_kind) THEN 1 ELSE 0 END) AS valid
 RETURN count(*),sum(CASE WHEN total=size(item.edges) AND distinct_edges=total AND valid=total THEN 1 ELSE 0 END)`, p)
			if err != nil {
				return err
			}
			if row.Values[0] != int64(len(expected)) || row.Values[1] != int64(len(expected)) {
				return ErrGraphConflict
			}
		}
		return r.verifySeal(ctx, tx)
	})
}
