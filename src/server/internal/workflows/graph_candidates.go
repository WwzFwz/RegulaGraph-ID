// Runs a graph candidate branch under a published graph/index pin. Seed resolution
// is an explicit read-only query port; traversal and source lookup share the same
// admitted generation. Candidate ordering is deterministic source-ID order, not
// semantic confidence. No lookup overflow silently truncates path source text.
// Measure seed/traversal/lookup stage latency, fan-out and path recall under the
// required benchmark suite; this branch alone does not prove model quality.
package workflows

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/graph"
)

type GraphSeedResolver func(context.Context, string, *domain.PinnedGraph) ([]string, error)
type GraphCandidateSearch struct {
	Catalog   GraphEvidenceCatalog
	Backend   *neo4j.Store
	Index     *domain.PinnedIndex
	Lookup    GraphSourceLookup
	Seeds     GraphSeedResolver
	Traversal graph.TraversalConfig
}

// Reauthorize is called after hydration/reranking and after generation, while
// the owning session still holds the same lease. Backend errors never downgrade
// the requested profile or turn already generated text into an authorized result.
func (g *GraphCandidateSearch) Reauthorize(ctx context.Context, input retrieval.SearchInput) error {
	if ctx == nil || g == nil || g.Catalog == nil || g.Index == nil || input.Context == nil {
		return errors.New("pinned graph authority required")
	}
	if err := domain.ValidateWire(input.Context, domain.DefaultWireLimits); err != nil {
		return err
	}
	call, cancel := context.WithDeadline(ctx, g.Index.Pin.ExpiresAt)
	defer cancel()
	call, cancel = context.WithDeadline(call, input.Context.Deadline.AsTime())
	defer cancel()
	view, err := g.Catalog.LoadPinnedGraph(call, g.Index.Pin, input.Context.AuthScopeRef)
	if err != nil {
		return err
	}
	if view == nil || view.AuthScope != input.Context.AuthScopeRef || !proto.Equal(view.Snapshot, g.Index.Snapshot) || !proto.Equal(view.Snapshot, input.Context.SnapshotRef) {
		return domain.ErrPersistentIntegrity
	}
	return call.Err()
}

func (g *GraphCandidateSearch) Search(ctx context.Context, input retrieval.SearchInput) (*retrieval.BranchOutput, error) {
	started := time.Now()
	if ctx == nil || g == nil || g.Catalog == nil || g.Backend == nil || g.Index == nil || g.Index.Snapshot == nil || g.Lookup == nil || g.Seeds == nil || input.Context == nil || input.Scope.Limit < 1 || input.Scope.Limit > 256 {
		return nil, errors.New("configured pinned graph branch required")
	}
	if err := g.Traversal.Validate(); err != nil {
		return nil, err
	}
	if len(input.Question) > 64<<10 || !utf8.ValidString(input.Question) || strings.TrimSpace(input.Question) == "" {
		return nil, errors.New("bounded graph query text required")
	}
	if err := domain.ValidateWire(input.Context, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if !proto.Equal(input.Context.SnapshotRef, g.Index.Snapshot) || input.Context.CorpusId != g.Index.Pin.CorpusID || input.Scope.SnapshotSeq != g.Index.Snapshot.Sequence || !proto.Equal(input.Generation, g.Index.Binding.Generation) || input.Scope.ProvisionVersionID != "" {
		return nil, errors.New("graph query differs from admitted index")
	}
	bound := g.Lookup.Binding()
	if bound.CorpusID != g.Index.Pin.CorpusID || bound.Collection != g.Index.Binding.Collection || !proto.Equal(bound.Generation, input.Generation) {
		return nil, errors.New("graph lookup binding differs")
	}
	call, cancel := context.WithDeadline(ctx, g.Index.Pin.ExpiresAt)
	defer cancel()
	call, cancel = context.WithDeadline(call, input.Context.Deadline.AsTime())
	defer cancel()
	view, err := g.Catalog.LoadPinnedGraph(call, g.Index.Pin, input.Context.AuthScopeRef)
	if err != nil {
		return nil, err
	}
	if view == nil || view.AuthScope != input.Context.AuthScopeRef || !proto.Equal(view.Snapshot, g.Index.Snapshot) {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateGraphCatalogBinding(view.Catalog); err != nil {
		return nil, err
	}
	seedView := *view
	seedView.Snapshot = proto.Clone(view.Snapshot).(*pb.SnapshotRef)
	seedView.Catalog.Binding.BaseSnapshot = proto.Clone(view.Catalog.Binding.BaseSnapshot).(*pb.SnapshotRef)
	seedView.Catalog.Outputs = nil
	for _, ref := range view.Catalog.Outputs {
		seedView.Catalog.Outputs = append(seedView.Catalog.Outputs, proto.Clone(ref).(*pb.ArtifactRef))
	}
	seeds, err := g.Seeds(call, input.Question, &seedView)
	if err != nil {
		return nil, err
	}
	paths := &graph.TraversalResult{Snapshot: proto.Clone(view.Snapshot).(*pb.SnapshotRef), FrontierExhausted: false, StopReasons: []string{"query_seed_unresolved"}}
	if len(seeds) > 0 {
		reader, err := g.Backend.OpenReader(call, view)
		if err != nil {
			return nil, err
		}
		paths, err = graph.Traverse(call, reader, view.Snapshot, seeds, g.Traversal)
		if err != nil {
			return nil, err
		}
	}
	refs, err := graph.SourcesForPaths(paths, 128)
	if err != nil {
		return nil, err
	}
	var hits []qdrant.Hit
	if len(refs) > 0 {
		hits, err = g.Lookup.FindSourceChunks(call, refs, input.Scope)
		if err != nil {
			return nil, err
		}
	}
	if len(hits) > input.Scope.Limit {
		return nil, domain.ErrGraphReadBudget
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].RecordID < hits[j].RecordID })
	out := &retrieval.BranchOutput{Ranking: retrieval.RankedBranch{Kind: pb.RetrieverKind_RETRIEVER_KIND_GRAPH}, Hits: hits, Graph: paths}
	for i, hit := range hits {
		candidate := &pb.Candidate{EvidenceKey: hit.RecordID, Retriever: pb.RetrieverKind_RETRIEVER_KIND_GRAPH, Rank: uint32(i + 1), Representation: "graph-source-id-order-v1", FilterDecisions: []*pb.FilterDecision{{Rule: "admitted-graph-source-lookup", Accepted: true, Reason: "Pinned graph support source lookup; text and legal-time verification follow hydration"}}}
		if err := domain.ValidateWire(candidate, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		out.Ranking.Candidates = append(out.Ranking.Candidates, candidate)
	}
	final, err := g.Catalog.LoadPinnedGraph(call, g.Index.Pin, input.Context.AuthScopeRef)
	if err != nil {
		return nil, err
	}
	if final == nil || !proto.Equal(final.Snapshot, view.Snapshot) || final.AuthScope != view.AuthScope || final.Catalog.BindingHash != view.Catalog.BindingHash || final.Catalog.OperationsHash != view.Catalog.OperationsHash {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	out.Duration = time.Since(started)
	return out, nil
}

func cloneTraversal(in *graph.TraversalResult) *graph.TraversalResult {
	if in == nil {
		return nil
	}
	out := *in
	out.Snapshot = proto.Clone(in.Snapshot).(*pb.SnapshotRef)
	out.StopReasons = append([]string(nil), in.StopReasons...)
	out.Paths = nil
	for _, p := range in.Paths {
		out.Paths = append(out.Paths, proto.Clone(p).(*pb.GraphPath))
	}
	out.Assertions = map[string]*pb.RelationAssertion{}
	for id, v := range in.Assertions {
		out.Assertions[id] = proto.Clone(v).(*pb.RelationAssertion)
	}
	out.Supports = map[string]*pb.SupportRecord{}
	for id, v := range in.Supports {
		out.Supports[id] = proto.Clone(v).(*pb.SupportRecord)
	}
	out.Entities = map[string]*pb.CanonicalEntity{}
	for id, v := range in.Entities {
		out.Entities[id] = proto.Clone(v).(*pb.CanonicalEntity)
	}
	return &out
}

func validateCandidateTraversal(in *graph.TraversalResult) error {
	if in == nil {
		return errors.New("graph traversal required")
	}
	if len(in.Entities) > 100000 || len(in.Assertions) > 100000 || len(in.Supports) > 100000 || len(in.StopReasons) > 32 {
		return domain.ErrGraphReadBudget
	}
	var bytes int
	for _, reason := range in.StopReasons {
		if len(reason) == 0 || len(reason) > 256 {
			return domain.ErrGraphReadBudget
		}
		bytes += len(reason)
	}
	if _, err := graph.SourcesForPaths(in, 128); err != nil {
		return err
	}
	check := func(id string, meta *pb.RecordMeta, value proto.Message) error {
		if err := domain.ValidateWire(value, domain.DefaultWireLimits); err != nil {
			return err
		}
		if meta == nil || meta.RecordId != id || meta.SchemaVersion != 1 || meta.CorpusId != in.Snapshot.CorpusId || meta.Visibility == nil || meta.Visibility.FromSeq != in.Snapshot.Sequence || meta.Visibility.ToSeq != nil {
			return errors.New("graph candidate record scope differs")
		}
		bytes += proto.Size(value)
		if bytes > 16<<20 {
			return domain.ErrGraphReadBudget
		}
		return nil
	}
	for _, p := range in.Paths {
		bytes += proto.Size(p)
		if bytes > 16<<20 {
			return domain.ErrGraphReadBudget
		}
	}
	for id, v := range in.Entities {
		if v == nil {
			return errors.New("nil graph candidate entity")
		}
		if err := check(id, v.Meta, v); err != nil {
			return err
		}
	}
	for id, v := range in.Assertions {
		if v == nil {
			return errors.New("nil graph candidate assertion")
		}
		if err := check(id, v.Meta, v); err != nil {
			return err
		}
	}
	for id, v := range in.Supports {
		if v == nil {
			return errors.New("nil graph candidate support")
		}
		if err := check(id, v.Meta, v); err != nil {
			return err
		}
	}
	return nil
}
