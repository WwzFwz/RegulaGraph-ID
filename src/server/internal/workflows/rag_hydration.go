// Adapts the storage-backed evidence hydrator to the RAG workflow callback.
// Snapshot equality and fused order are checked before resolving source text;
// the existing RAG boundary rebuilds ranking provenance afterward. No database
// or model implementation is copied into orchestration. Measure this stage with
// the same pinned corpus/model and benchmark-targets.yaml; quality is unmeasured.
package workflows

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/retrieval"
)

func StorageCandidateHydrator(h *retrieval.SourceHydrator, snapshot *pb.SnapshotRef) (CandidateHydrator, error) {
	if h == nil || snapshot == nil {
		return nil, errors.New("storage hydrator and pinned snapshot required")
	}
	owned := proto.Clone(snapshot).(*pb.SnapshotRef)
	return func(ctx context.Context, request *pb.QuestionRequest, found *CandidateSearchResult) (*HydratedCandidates, error) {
		if found == nil || !proto.Equal(found.Snapshot, owned) {
			return nil, errors.New("candidate snapshot differs from storage pin")
		}
		hits := make([]qdrant.Hit, 0, len(found.Candidates))
		for _, candidate := range found.Candidates {
			hit, ok := found.Hits[candidate.EvidenceKey]
			if !ok {
				return nil, errors.New("fused candidate lacks backend identity")
			}
			hits = append(hits, hit)
		}
		result, err := h.Hydrate(ctx, request, hits)
		if err != nil {
			return nil, err
		}
		return &HydratedCandidates{Evidence: result.Evidence, Rejected: result.Rejected, SourceURLs: result.SourceURLs}, nil
	}, nil
}
