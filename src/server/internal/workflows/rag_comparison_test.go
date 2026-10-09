// Tests comparison orchestration with synthetic storage/discovery/hydration.
// One pin/discovery serves independently dated buckets, with all-or-error
// semantics and no generation. Graph projection uses production date checks;
// this is not a real-corpus or performance acceptance test.
package workflows

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
)

func TestRAGComparisonOnePinAndDiscovery(t *testing.T) {
	for _, mode := range []string{"success", "hybrid", "graph", "graph only", "second date failure", "second reranker failure", "cancelled", "lease revoked", "cleanup failure", "duplicate date", "historical mismatch", "byte budget"} {
		t.Run(mode, func(t *testing.T) {
			isGraph := mode == "graph" || mode == "graph only"
			isSuccess := mode == "success" || mode == "hybrid" || isGraph
			w, request, input, provider := ragFixture(t)
			if isGraph {
				w, request, input, provider, _ = graphRAGFixture(t)
			}
			if mode == "hybrid" {
				request.RequestedProfile = pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG
			}
			if mode == "graph only" {
				request.RequestedProfile = pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG
			}
			reranker := &workflowReranker{}
			if isSuccess || mode == "second reranker failure" {
				hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
				var err error
				w.Reranker, err = retrieval.NewEvidenceReranker(reranker, retrieval.EvidenceRerankConfig{Model: &pb.ModelManifest{ModelId: "model:rerank", Version: "v1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK, MaxTokens: 512, Precision: "fp32", Backend: "fixture"}, MaximumCandidates: 10, PairsPerBatch: 2, MaximumRequestBytes: 4 << 20})
				if err != nil {
					t.Fatal(err)
				}
			}
			request.TemporalScope.Mode = pb.TemporalMode_TEMPORAL_MODE_COMPARE
			request.TemporalScope.EffectiveAt = nil
			request.TemporalScope.CompareDates = []*pb.CalendarDate{{Year: 2026, Month: 1, Day: 1}, {Year: 2025, Month: 1, Day: 1}}
			if mode == "duplicate date" {
				request.TemporalScope.CompareDates[1].Year = 2026
			}
			if mode == "byte budget" {
				for len(request.TemporalScope.CompareDates) < 8 {
					request.TemporalScope.CompareDates = append(request.TemporalScope.CompareDates, &pb.CalendarDate{Year: int32(2000 + len(request.TemporalScope.CompareDates)), Month: 1, Day: 1})
				}
			}
			if mode == "historical mismatch" {
				request.SnapshotId = proto.String("snapshot:old")
			}
			store := &ragLeaseStore{index: &domain.PinnedIndex{Snapshot: proto.Clone(input.Context.SnapshotRef).(*pb.SnapshotRef), Binding: domain.IndexCatalogBinding{PublicationID: "publication:one", Fence: 1, Endpoint: "http://fixture", Collection: "fixture", Generation: sessionGeneration()}}}
			if mode == "lease revoked" {
				store.failRead = 2
			}
			if mode == "cleanup failure" {
				store.releaseErr = errors.New("cleanup failed")
			}
			call := proto.Clone(input.Context).(*pb.RequestContext)
			call.SnapshotRef = nil
			var branches atomic.Int32
			wrap := func(original CandidateBranch) CandidateBranch {
				return func(c context.Context, in retrieval.SearchInput) (*retrieval.BranchOutput, error) {
					branches.Add(1)
					return original(c, in)
				}
			}
			w.Search.Dense = wrap(w.Search.Dense)
			w.Search.Lexical = wrap(w.Search.Lexical)
			if isGraph {
				w.Search.Graph = wrap(w.Search.Graph)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hydrate := w.Hydrate
			dates := []int32{}
			w.Hydrate = func(c context.Context, q *pb.QuestionRequest, found *CandidateSearchResult) (*HydratedCandidates, error) {
				if store.releases != 0 || q.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF {
					t.Fatal("lost shared lease or unresolved date")
				}
				dates = append(dates, q.TemporalScope.EffectiveAt.Year)
				if len(dates) == 2 && mode == "second reranker failure" {
					reranker.fail = true
				}
				if len(dates) == 2 && mode == "second date failure" {
					return nil, errors.New("second date failed")
				}
				if len(dates) == 2 && mode == "cancelled" {
					cancel()
					return nil, c.Err()
				}
				out, err := hydrate(c, q, found)
				if mode == "byte budget" {
					out.Evidence.Items[0].Text = strings.Repeat("x", 2<<20)
				}
				return out, err
			}
			factoryCalls := 0
			session := &RAGSession{Store: store, OwnerID: "reader:test", MaximumDuration: 20 * time.Second, SearchLimit: 10, Clock: func() time.Time { t.Fatal("COMPARE must not read calendar clock"); return time.Time{} }, Factory: func(context.Context, *domain.PinnedIndex) (*RAGWorkflow, error) { factoryCalls++; return w, nil }}
			before := proto.Clone(request)
			result, err := session.CompareEvidence(ctx, request, call)
			if !proto.Equal(before, request) || call.SnapshotRef != nil {
				t.Fatal("mutated caller")
			}
			if isSuccess {
				if err != nil || result == nil || len(result.Dates) != 2 || len(dates) != 2 || dates[0] != 2026 || dates[1] != 2025 {
					t.Fatal(result, err, dates)
				}
				wantBranches := int32(1)
				if isGraph {
					wantBranches = 3
				}
				if mode == "hybrid" {
					wantBranches = 2
				}
				if mode == "graph only" {
					wantBranches = 1
				}
				if store.pins != 1 || factoryCalls != 1 || branches.Load() != wantBranches || provider.calls != 0 {
					t.Fatal("repeated setup/discovery or generated comparison", store.pins, factoryCalls, branches.Load(), provider.calls)
				}
				if reranker.calls != 2 || result.Dates[0].Result.Reranking == nil || result.Dates[1].Result.Reranking == nil {
					t.Fatal("did not rerank each dated evidence bucket", reranker.calls)
				}
				for i, entry := range result.Dates {
					if !proto.Equal(entry.Date, request.TemporalScope.CompareDates[i]) || !proto.Equal(entry.Result.Evidence.Snapshot, result.Snapshot) || !proto.Equal(entry.Result.Temporal.EffectiveDate, entry.Date) {
						t.Fatal("bucket scope drift")
					}
				}
				if isGraph {
					missing := strings.Join(result.Dates[1].Result.Evidence.MissingDependencies, " ")
					if !strings.Contains(missing, "graph-applicability") {
						t.Fatal("lost per-date unresolved graph temporal obligation", missing)
					}
				}
				result.Dates[0].Result.Evidence.Items[0].Text = "mutated"
				if result.Dates[1].Result.Evidence.Items[0].Text == "mutated" {
					t.Fatal("date buckets alias evidence")
				}
			} else if err == nil || result != nil {
				t.Fatal("failed comparison returned partial success", result, err)
			}
			if mode == "byte budget" && (!strings.Contains(err.Error(), "aggregate evidence byte budget") || len(dates) != 8) {
				t.Fatal("did not exercise aggregate budget", err, len(dates))
			}
			if mode == "second reranker failure" && (reranker.calls != 2 || provider.calls != 0) {
				t.Fatal("lost reranker failure or generated fallback")
			}
			if store.pins != store.releases {
				t.Fatal("comparison leaked snapshot lease")
			}
			if mode == "duplicate date" && store.pins != 0 {
				t.Fatal("invalid comparison acquired lease")
			}
		})
	}
}
