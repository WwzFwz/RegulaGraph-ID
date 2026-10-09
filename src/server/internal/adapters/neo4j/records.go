// Projects an admitted additive C01 GraphDelta into immutable Neo4j records and
// directed adjacency. Wire payloads remain byte-exact for evidence reconstruction;
// searchable properties and edges are verified against the same projection.
// One delta is bounded by C01 bytes/items, one generation by 64MiB/256 deltas.
// This backend projection does not replace source/ontology/registry admission.
// Initial generation only: closures/support changes and derived profiles/aliases
// are rejected explicitly until their publication lifecycle is integrated.
package neo4j

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type record struct {
	id, kind, hash string
	props          map[string]any
}
type edge struct{ from, to, kind, fromKind, toKind string }
type projection struct {
	id, hash string
	bytes    int64
	records  []record
	edges    []edge
}
type graphRecord interface {
	proto.Message
	GetMeta() *pb.RecordMeta
}

func (s *Store) project(delta *pb.GraphDelta) (projection, error) {
	var p projection
	if err := domain.ValidateWire(delta, domain.DefaultWireLimits); err != nil {
		return p, err
	}
	if delta.Meta.SchemaVersion != 1 || delta.Meta.CorpusId != s.binding.CorpusID || !proto.Equal(delta.BaseSnapshot, s.binding.BaseSnapshot) || delta.RegistryRevision != s.binding.RegistryRevision ||
		!delta.ValidationReport.Valid || len(delta.ValidationReport.Issues) != 0 || len(delta.Aliases) != 0 || len(delta.Profiles) != 0 || len(delta.VisibilityClosures) != 0 || len(delta.SupportChanges) != 0 {
		return p, errors.New("initial graph requires exact binding and complete additive delta")
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(delta)
	if err != nil {
		return p, err
	}
	p.id, p.hash = delta.Meta.RecordId, fmt.Sprintf("%x", sha256.Sum256(raw))
	p.bytes = int64(len(raw))
	seen := map[string]string{p.id: "delta"}
	add := func(kind string, value graphRecord, fields map[string]any) error {
		meta := value.GetMeta()
		if meta.SchemaVersion != 1 || meta.CorpusId != s.binding.CorpusID || meta.Visibility == nil || meta.Visibility.FromSeq != s.binding.Sequence || meta.Visibility.ToSeq != nil || seen[meta.RecordId] != "" {
			return ErrGraphConflict
		}
		seen[meta.RecordId] = kind
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(value)
		if e != nil {
			return e
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(raw))
		if fields == nil {
			fields = map[string]any{}
		}
		fields["id"], fields["corpus"], fields["generation"], fields["kind"], fields["hash"], fields["payload"], fields["from_seq"] = meta.RecordId, s.binding.CorpusID, s.binding.Generation, kind, digest, raw, int64(s.binding.Sequence)
		p.records = append(p.records, record{meta.RecordId, kind, digest, fields})
		return nil
	}
	for _, v := range delta.Entities {
		if v.RegistryRevision > s.binding.RegistryRevision {
			return p, ErrGraphConflict
		}
		if err = add("entity", v, map[string]any{"entity_type": v.EntityType, "label": v.PreferredLabel, "scope": v.Scope}); err != nil {
			return p, err
		}
	}
	for _, v := range delta.Mentions {
		if err = add("mention", v, nil); err != nil {
			return p, err
		}
	}
	for _, v := range delta.Assertions {
		if v.OntologyVersion != delta.OntologyVersion {
			return p, ErrGraphConflict
		}
		if err = add("assertion", v, map[string]any{"predicate": v.PredicateId, "subject": v.SubjectId, "object": v.ObjectId}); err != nil {
			return p, err
		}
		p.edges = append(p.edges, edge{v.Meta.RecordId, v.SubjectId, "RG_SUBJECT", "assertion", "entity"}, edge{v.Meta.RecordId, v.ObjectId, "RG_OBJECT", "assertion", "entity"})
		for _, id := range v.ExceptionRefs {
			p.edges = append(p.edges, edge{v.Meta.RecordId, id, "RG_EXCEPTION", "assertion", "assertion"})
		}
		for _, q := range v.Qualifiers {
			if id := q.GetCanonicalId(); id != "" {
				p.edges = append(p.edges, edge{v.Meta.RecordId, id, "RG_QUALIFIER", "assertion", "entity"})
			}
		}
	}
	supported := map[string]bool{}
	for _, v := range delta.Supports {
		if err = add("support", v, map[string]any{"assertion": v.AssertionId, "source_group": v.IndependentSourceGroup}); err != nil {
			return p, err
		}
		p.edges = append(p.edges, edge{v.Meta.RecordId, v.AssertionId, "RG_SUPPORT", "support", "assertion"})
		supported[v.AssertionId] = true
	}
	for _, v := range delta.Decisions {
		if v.RegistryRevision > s.binding.RegistryRevision {
			return p, ErrGraphConflict
		}
		if err = add("decision", v, nil); err != nil {
			return p, err
		}
		for _, id := range v.AssignedCanonicalIds {
			p.edges = append(p.edges, edge{v.Meta.RecordId, id, "RG_ASSIGNED", "decision", "entity"})
		}
	}
	if delta.ValidationReport.CheckedRecords != uint64(len(p.records)+1) {
		return p, ErrGraphConflict
	}
	for _, v := range delta.Assertions {
		if !supported[v.Meta.RecordId] {
			return p, errors.New("graph assertion has no support")
		}
	}
	unique := map[edge]bool{}
	edges := p.edges[:0]
	for _, e := range p.edges {
		if seen[e.from] != e.fromKind || seen[e.to] != e.toKind {
			return p, errors.New("graph adjacency endpoint missing or wrong type")
		}
		if !unique[e] {
			edges = append(edges, e)
			unique[e] = true
		}
	}
	p.edges = edges
	sort.Slice(p.records, func(i, j int) bool { return p.records[i].id < p.records[j].id })
	sort.Slice(p.edges, func(i, j int) bool {
		a, b := p.edges[i], p.edges[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if a.from != b.from {
			return a.from < b.from
		}
		return a.to < b.to
	})
	return p, nil
}
