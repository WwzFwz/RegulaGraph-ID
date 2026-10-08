// Verifies optional native reranking participates in the same hydrated evidence
// path for search and answers, and a model failure stops generation. Synthetic
// model responses establish orchestration behavior only, not ranking quality.
package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval"
)

type workflowReranker struct {
	calls int
	fail  bool
}

func (r *workflowReranker) RerankBatch(_ context.Context, req *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
	r.calls++
	if r.fail {
		return nil, errors.New("native unavailable")
	}
	response := &pb.RerankBatchResponse{RequestId: req.Context.RequestId, Model: proto.Clone(req.Model).(*pb.ModelManifest)}
	for _, pair := range req.Pairs {
		response.Results = append(response.Results, &pb.RerankItemResult{PairId: pair.PairId, Result: &pb.RerankItemResult_Score{Score: &pb.RerankScore{Score: 0.5, InputTokens: 10, Truncation: &pb.TruncationInfo{OriginalTokens: 10, RetainedTokens: 10}}}})
	}
	return response, nil
}
func TestRAGWorkflowReranksAuthenticatedEvidenceBeforeAnswer(t *testing.T) {
	for _, fail := range []bool{false, true} {
		w, request, input, provider := ragFixture(t)
		client := &workflowReranker{fail: fail}
		hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
		var err error
		w.Reranker, err = retrieval.NewEvidenceReranker(client, retrieval.EvidenceRerankConfig{Model: &pb.ModelManifest{ModelId: "model:rerank", Version: "v1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK, MaxTokens: 512, Precision: "fp32", Backend: "fixture"}, MaximumCandidates: 10, PairsPerBatch: 2, MaximumRequestBytes: 4 << 20})
		if err != nil {
			t.Fatal(err)
		}
		result, err := w.AnswerPinnedQuestion(context.Background(), request, input)
		if fail {
			if err == nil || result != nil || provider.calls != 0 || client.calls != 1 {
				t.Fatal("reranker failure silently bypassed")
			}
			continue
		}
		if err != nil || result.Reranking == nil || len(result.Reranking.Scores) != 1 || provider.calls != 1 || client.calls != 1 {
			t.Fatal("reranker not integrated", err)
		}
		if result.Reranking.Scores[0].PairId != result.Answer.Context.OrderedEvidenceIds[0] {
			t.Fatal("answer context lost ranked identity")
		}
		if _, err = w.SearchPinnedQuestion(context.Background(), request, input); err != nil || client.calls != 2 || provider.calls != 1 {
			t.Fatal("search path differs from answering", err)
		}
	}
}
