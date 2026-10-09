// Retrieves independently dated evidence under one authorized corpus snapshot.
// One candidate discovery is reused; each date gets its own temporal hydration,
// path projection and reranking. Date buckets are never merged into one bundle.
// All buckets must succeed, fit the aggregate byte cap and retain the same pin;
// failure/revocation/timeout discards the complete result. This is the evidence
// foundation for comparative answering, not an implemented synthesis generator.
// Measure per-date evidence coverage, shared discovery, total/queue p95/p99 and
// memory under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package workflows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/query"
)

const MaximumComparisonEvidenceBytes = 16 << 20

type DatedEvidence struct {
	Date   *pb.CalendarDate
	Result *RAGResult
}

type RAGComparisonResult struct {
	Snapshot *pb.SnapshotRef
	Profile  pb.RetrievalProfile
	Dates    []DatedEvidence
	Duration time.Duration
}

// CompareEvidence is a library operation. Transport/generation integration must
// retain each date bucket and its omissions instead of flattening evidence.
func (s *RAGSession) CompareEvidence(ctx context.Context, request *pb.QuestionRequest, call *pb.RequestContext) (*RAGComparisonResult, error) {
	if request == nil {
		return nil, errors.New("comparison request required")
	}
	scopes, err := query.PlanComparisonScopes(request.TemporalScope)
	if err != nil {
		return nil, err
	}
	return withRAGSnapshot(ctx, s, request, call, func(c context.Context, w *RAGWorkflow, request *pb.QuestionRequest, input retrieval.SearchInput) (*RAGComparisonResult, error) {
		started := time.Now()
		out := &RAGComparisonResult{Snapshot: proto.Clone(input.Context.SnapshotRef).(*pb.SnapshotRef), Profile: request.RequestedProfile}
		var discovery *CandidateSearchResult
		remaining := MaximumComparisonEvidenceBytes
		for _, scope := range scopes {
			if err := c.Err(); err != nil {
				return nil, err
			}
			child := proto.Clone(request).(*pb.QuestionRequest)
			child.TemporalScope = proto.Clone(scope).(*pb.TemporalScope)
			result, err := w.searchPinnedCandidates(c, child, input, discovery)
			if err != nil {
				return nil, fmt.Errorf("comparison date %04d-%02d-%02d: %w", scope.EffectiveAt.Year, scope.EffectiveAt.Month, scope.EffectiveAt.Day, err)
			}
			if result == nil || result.Evidence == nil || !proto.Equal(result.Evidence.Snapshot, out.Snapshot) {
				return nil, errors.New("comparison result lost pinned snapshot")
			}
			if discovery == nil {
				discovery = cloneCandidateSearch(result.Search)
			}
			size := proto.Size(result.Evidence)
			if size > remaining {
				return nil, errors.New("comparison aggregate evidence byte budget exceeded")
			}
			remaining -= size
			_, result.Temporal, err = query.ResolveTemporalScope(scope, nil, nil)
			if err != nil {
				return nil, err
			}
			out.Dates = append(out.Dates, DatedEvidence{Date: proto.Clone(scope.EffectiveAt).(*pb.CalendarDate), Result: result})
		}
		// Reauthorize graph after all dates, while the common snapshot lease lives.
		if w.GraphAdmission != nil {
			if err := w.GraphAdmission(c, input); err != nil {
				return nil, err
			}
		}
		out.Duration = time.Since(started)
		return out, nil
	})
}
