// Connects admitted graph discovery to exact source chunk lookup and the existing
// production hydrator. Graph records never supply answer text. Scope/pin are
// rechecked before and after source reads; index binding and snapshot stay fixed.
// Returned paths preserve missing dependencies for subsequent fusion/context.
// Paths must come from production Traverse with the admitted Neo4j reader, never
// a client/model DTO: rechecking admission does not authenticate arbitrary input
// assertion bytes. PathEvidence membership is an all-items context obligation.
// Measure Qdrant lookup + source hydration + mapping latency/bytes separately;
// fixtures do not establish the required quality or latency benchmark gates.
package workflows

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/graph"
)

type GraphEvidenceCatalog interface {
	retrieval.EvidenceCatalog
	LoadPinnedGraph(context.Context, domain.SnapshotPin, string) (*domain.PinnedGraph, error)
}
type GraphSourceLookup interface {
	Binding() qdrant.Binding
	FindSourceChunks(context.Context, []*pb.SourceVersionRef, qdrant.SearchScope) ([]qdrant.Hit, error)
}
type GraphEvidenceHydrator struct {
	Catalog   GraphEvidenceCatalog
	Artifacts retrieval.EvidenceArtifactReader
	Lookup    GraphSourceLookup
	Config    retrieval.HydrationConfig
}
type HydratedGraph struct {
	Mapping    *graph.EvidenceMapping
	Hits       []qdrant.Hit
	Rejected   map[string]string
	SourceURLs domain.SourceURLLookup
}

func (h *GraphEvidenceHydrator) Hydrate(ctx context.Context, request *pb.QuestionRequest, index *domain.PinnedIndex, scope string, paths *graph.TraversalResult) (*HydratedGraph, error) {
	if ctx == nil || h == nil || h.Catalog == nil || h.Artifacts == nil || h.Lookup == nil || index == nil || index.Snapshot == nil || paths == nil || !proto.Equal(paths.Snapshot, index.Snapshot) {
		return nil, errors.New("pinned graph/source hydration dependencies required")
	}
	if request == nil || domain.ValidateWire(request, domain.DefaultWireLimits) != nil {
		return nil, errors.New("valid graph question required")
	}
	ctx, cancel := context.WithDeadline(ctx, index.Pin.ExpiresAt)
	defer cancel()
	view, err := h.Catalog.LoadPinnedGraph(ctx, index.Pin, scope)
	if err != nil {
		return nil, err
	}
	if view == nil || view.AuthScope != scope || !proto.Equal(view.Snapshot, index.Snapshot) {
		return nil, domain.ErrPersistentIntegrity
	}
	binding := h.Lookup.Binding()
	if binding.CorpusID != index.Snapshot.CorpusId || binding.Collection != index.Binding.Collection || !proto.Equal(binding.Generation, index.Binding.Generation) {
		return nil, errors.New("graph source lookup index differs from pin")
	}
	source, err := retrieval.NewSourceHydrator(h.Catalog, h.Artifacts, index, h.Config)
	if err != nil {
		return nil, err
	}
	refs, err := graph.SourcesForPaths(paths, 128)
	if err != nil {
		return nil, err
	}
	var hits []qdrant.Hit
	if len(refs) > 0 {
		hits, err = h.Lookup.FindSourceChunks(ctx, refs, qdrant.SearchScope{SnapshotSeq: index.Snapshot.Sequence, Limit: h.Config.MaximumCandidates})
		if err != nil {
			return nil, err
		}
	}
	hydrated, err := source.Hydrate(ctx, request, hits)
	if err != nil {
		return nil, err
	}
	mapping, err := graph.AttachEvidence(paths, request, hydrated.Evidence, h.Config.MaximumEvidenceBytes)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, item := range mapping.Bundle.Items {
		used[item.Meta.RecordId] = true
	}
	result := &HydratedGraph{Mapping: mapping, Rejected: hydrated.Rejected, SourceURLs: hydrated.SourceURLs}
	for _, hit := range hits {
		if used[hit.RecordID] {
			result.Hits = append(result.Hits, hit)
		} else if result.Rejected[hit.RecordID] == "" {
			result.Rejected[hit.RecordID] = "outside_graph_support_spans"
		}
	}
	final, err := h.Catalog.LoadPinnedGraph(ctx, index.Pin, scope)
	if err != nil {
		return nil, err
	}
	if final == nil || final.AuthScope != scope || !proto.Equal(final.Snapshot, view.Snapshot) || final.Catalog.BindingHash != view.Catalog.BindingHash || final.Catalog.OperationsHash != view.Catalog.OperationsHash {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
