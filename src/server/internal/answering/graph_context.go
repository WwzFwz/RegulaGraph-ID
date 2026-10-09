// Renders admitted graph paths alongside their authenticated source evidence.
// This local immutable plan is not a wire schema or an authorization token:
// callers must supply production traversal/mapping and hold the read lease.
// Every path keeps all mapped text members; losing any member or its rendered
// anchor makes the path omitted. Exact tokenizer accounting includes graph JSON.
// Bound serialized plan/input bytes and measure token inflation, retained path
// coverage and p95/p99 against configs/benchmark-targets.yaml (unmeasured).
package answering

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/graph"
)

// GraphContext owns rendering and identity fingerprints, allowing rank changes
// while rejecting substituted or removed evidence before context selection.
type GraphContext struct {
	snapshot *pb.SnapshotRef
	items    map[string][32]byte
	paths    map[string]*pb.GraphPath
	members  map[string][]string
	blocks   map[string]string
	header   [32]byte
}

// NewGraphContext consumes trusted Traverse/AttachEvidence output. It verifies
// integration closure, not whether an LLM relation semantically follows the law.
func NewGraphContext(traversal *graph.TraversalResult, mapping *graph.EvidenceMapping) (*GraphContext, error) {
	if mapping == nil || mapping.Bundle == nil || traversal == nil || !proto.Equal(traversal.Snapshot, mapping.Bundle.Snapshot) {
		return nil, errors.New("matching admitted graph and evidence required")
	}
	if _, err := graph.SourcesForPaths(traversal, 128); err != nil {
		return nil, err
	}
	bundle := mapping.Bundle
	if err := domain.ValidateWire(bundle, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if proto.Size(bundle) > 4<<20 {
		return nil, domain.ErrGraphReadBudget
	}
	g := &GraphContext{snapshot: proto.Clone(bundle.Snapshot).(*pb.SnapshotRef), items: map[string][32]byte{}, paths: map[string]*pb.GraphPath{}, members: map[string][]string{}, blocks: map[string]string{}}
	g.header = bundleHeaderFingerprint(bundle)
	blocks := map[string][]string{}
	byID := map[string]*pb.Evidence{}
	indexed := map[string]map[string]*pb.GraphPath{}
	for _, item := range bundle.Items {
		byID[item.Meta.RecordId] = item
		g.items[item.Meta.RecordId] = evidenceFingerprint(item)
		for _, path := range item.GraphPaths {
			if indexed[path.PathId] == nil {
				indexed[path.PathId] = map[string]*pb.GraphPath{}
			}
			if indexed[path.PathId][item.Meta.RecordId] != nil {
				return nil, errors.New("duplicate path on evidence")
			}
			indexed[path.PathId][item.Meta.RecordId] = path
		}
	}
	var total int
	for _, discovered := range traversal.Paths {
		members := append([]string(nil), mapping.PathEvidence[discovered.PathId]...)
		actual := indexed[discovered.PathId]
		if len(members) != len(actual) {
			return nil, errors.New("graph plan omitted mapped source member")
		}
		if len(members) == 0 {
			continue
		} // Unhydrated paths stay omitted.
		sort.Strings(members)
		var path *pb.GraphPath
		for i, id := range members {
			if byID[id] == nil || i > 0 && members[i-1] == id {
				return nil, errors.New("invalid graph text membership")
			}
			found := actual[id]
			if found == nil || path != nil && !proto.Equal(path, found) {
				return nil, errors.New("graph membership lost path definition")
			}
			path = found
		}
		check := proto.Clone(path).(*pb.GraphPath)
		check.Coverage = discovered.Coverage
		if !proto.Equal(check, discovered) {
			return nil, errors.New("hydrated path differs from traversal")
		}
		projection := struct {
			Path             *pb.GraphPath           `json:"path"`
			Entities         []*pb.CanonicalEntity   `json:"entities"`
			Assertions       []*pb.RelationAssertion `json:"assertions"`
			Supports         []*pb.SupportRecord     `json:"supports"`
			RequiredEvidence []string                `json:"required_evidence_ids"`
		}{Path: path, RequiredEvidence: members}
		for _, id := range path.OrderedNodeIds {
			entity := traversal.Entities[id]
			if entity == nil || domain.ValidateWire(entity, domain.DefaultWireLimits) != nil || entity.Meta.RecordId != id || entity.Meta.CorpusId != bundle.Meta.CorpusId || entity.Meta.Visibility == nil || entity.Meta.Visibility.FromSeq != bundle.Snapshot.Sequence || entity.Meta.Visibility.ToSeq != nil {
				return nil, errors.New("graph entity rendering identity differs")
			}
			projection.Entities = append(projection.Entities, entity)
		}
		for i, id := range path.OrderedAssertionIds {
			projection.Assertions = append(projection.Assertions, traversal.Assertions[id])
			projection.Supports = append(projection.Supports, traversal.Supports[path.SelectedSupportIds[i]])
		}
		// Protobuf size bounds the projection before JSON allocation; JSON is
		// checked separately because escaping/field names can expand its size.
		inputSize := proto.Size(path)
		for _, v := range projection.Entities {
			inputSize += proto.Size(v)
		}
		for _, v := range projection.Assertions {
			inputSize += proto.Size(v)
		}
		for _, v := range projection.Supports {
			inputSize += proto.Size(v)
		}
		if inputSize > 4<<20-total {
			return nil, domain.ErrGraphReadBudget
		}
		raw, err := json.Marshal(projection)
		if err != nil {
			return nil, err
		}
		block := "\nGraph source relation (not a legal or semantic approval): " + string(raw)
		total += len(block)
		if total > 4<<20 {
			return nil, domain.ErrGraphReadBudget
		}
		g.paths[path.PathId] = proto.Clone(path).(*pb.GraphPath)
		g.members[path.PathId] = members
		blocks[members[0]] = append(blocks[members[0]], block)
	}
	for id, parts := range blocks {
		g.blocks[id] = strings.Join(parts, "")
	}
	return g, nil
}

func bundleHeaderFingerprint(bundle *pb.EvidenceBundle) [32]byte {
	// Clone only the small metadata shell, never the full evidence contents.
	header := &pb.EvidenceBundle{Meta: bundle.Meta, RequiredPathSets: bundle.RequiredPathSets, MissingDependencies: bundle.MissingDependencies, Completeness: bundle.Completeness, RetrievalManifest: bundle.RetrievalManifest, Snapshot: bundle.Snapshot, CompletionStatus: bundle.CompletionStatus, Errors: bundle.Errors}
	raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(header)
	return sha256.Sum256(raw)
}

func evidenceFingerprint(item *pb.Evidence) [32]byte {
	raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(item)
	return sha256.Sum256(raw)
}

func selectGraphContext(bundle *pb.EvidenceBundle, plans []*GraphContext) (*GraphContext, error) {
	if len(plans) > 1 {
		return nil, errors.New("at most one graph context plan allowed")
	}
	if len(plans) == 0 || plans[0] == nil {
		return nil, nil
	}
	g := plans[0]
	if !proto.Equal(bundle.Snapshot, g.snapshot) || len(bundle.Items) != len(g.items) || bundleHeaderFingerprint(bundle) != g.header {
		return nil, errors.New("graph context evidence changed")
	}
	for _, item := range bundle.Items {
		expected, ok := g.items[item.Meta.RecordId]
		if !ok || expected != evidenceFingerprint(item) {
			return nil, errors.New("graph context source content changed")
		}
	}
	return g, nil
}

func renderContextEvidence(item *pb.Evidence, g *GraphContext) string {
	block := renderEvidence(item)
	if g != nil {
		block += g.blocks[item.Meta.RecordId]
	}
	return block
}

func (g *GraphContext) covered(id string, selected map[string]bool) bool {
	if g == nil || g.paths[id] == nil || g.paths[id].Coverage != pb.Completeness_COMPLETENESS_COMPLETE {
		return false
	}
	for _, member := range g.members[id] {
		if !selected[member] {
			return false
		}
	}
	return len(g.members[id]) > 0
}

// A returned path is attached only when a claim names every required text
// member. This is structural traceability, not semantic support approval.
func (g *GraphContext) claimed(id string, claims []map[string]bool) bool {
	if g == nil || len(g.members[id]) == 0 {
		return false
	}
	for _, ids := range claims {
		if len(ids) < len(g.members[id]) {
			continue
		}
		all := true
		for _, v := range g.members[id] {
			if !ids[v] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func claimMembership(claims []*pb.Claim) []map[string]bool {
	out := make([]map[string]bool, 0, len(claims))
	for _, claim := range claims {
		ids := map[string]bool{}
		for _, id := range claim.EvidenceIds {
			ids[id] = true
		}
		out = append(out, ids)
	}
	return out
}

func (g *GraphContext) answerPaths(selectedIDs []string, claims []*pb.Claim) []*pb.GraphPath {
	if g == nil {
		return nil
	}
	selected := map[string]bool{}
	for _, id := range selectedIDs {
		selected[id] = true
	}
	ids := make([]string, 0, len(g.paths))
	for id := range g.paths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var paths []*pb.GraphPath
	claimSets := claimMembership(claims)
	for _, id := range ids {
		if g.covered(id, selected) && g.claimed(id, claimSets) {
			paths = append(paths, proto.Clone(g.paths[id]).(*pb.GraphPath))
		}
	}
	return paths
}
