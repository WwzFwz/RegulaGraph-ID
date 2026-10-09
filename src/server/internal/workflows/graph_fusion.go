// Adds graph source/path obligations after shared catalog hydration and fusion,
// before reranking/context. Graph-only lookup false positives are explicitly
// rejected; dense/BM25 evidence is retained even when outside graph spans. All
// required path members survive ranking; only context packing may omit them with
// explicit reporting. No extra source reads or model calls occur here. Measure
// mapping time and retained multi-hop evidence under the required retrieval gates.
package workflows

import (
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/graph"
)

func mergeGraphEvidence(paths *graph.TraversalResult, request *pb.QuestionRequest, hydrated *HydratedCandidates) (*graph.EvidenceMapping, error) {
	if hydrated == nil || hydrated.Evidence == nil {
		return nil, errors.New("hydrated graph fusion evidence required")
	}
	mapping, err := graph.AttachEvidence(paths, request, hydrated.Evidence, 4<<20)
	if err != nil {
		return nil, err
	}
	byID := map[string]*pb.Evidence{}
	for _, item := range mapping.Bundle.Items {
		byID[item.Meta.RecordId] = item
	}
	if hydrated.Rejected == nil {
		hydrated.Rejected = map[string]string{}
	}
	items := make([]*pb.Evidence, 0, len(hydrated.Evidence.Items))
	for _, item := range hydrated.Evidence.Items {
		if mapped := byID[item.Meta.RecordId]; mapped != nil {
			items = append(items, mapped)
			continue
		}
		nonGraph := false
		for _, origin := range item.CandidateProvenance {
			nonGraph = nonGraph || origin.Retriever != pb.RetrieverKind_RETRIEVER_KIND_GRAPH
		}
		if nonGraph {
			items = append(items, proto.Clone(item).(*pb.Evidence))
		} else {
			hydrated.Rejected[item.Meta.RecordId] = "outside_graph_support_spans"
		}
	}
	mapping.Bundle.Items = items
	switch {
	case len(mapping.Bundle.MissingDependencies) > 0 || hydrated.Evidence.Completeness == pb.Completeness_COMPLETENESS_PARTIAL:
		mapping.Bundle.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	case len(items) == 0:
		mapping.Bundle.Completeness = pb.Completeness_COMPLETENESS_NONE
	default:
		mapping.Bundle.Completeness = hydrated.Evidence.Completeness
	}
	if proto.Size(mapping.Bundle) > 4<<20 {
		return nil, domain.ErrGraphReadBudget
	}
	if err = domain.ValidateWire(mapping.Bundle, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	return mapping, nil
}
