// Verifies aggregate diagnostics admission before retaining repeated model JSON.
// Small explicit budgets exercise the same encoder used by CLI and comparisons;
// fixtures demonstrate accounting boundaries, not production memory benchmarks.
package schemas

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/workflows"
	"strings"
	"testing"
	"time"
)

func rerankBudgetFixture() *workflows.RAGResult {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "reranker:fixture", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK, MaxTokens: 8192, Precision: "fp32", Backend: "fixture"}
	evidence := &pb.EvidenceBundle{Items: []*pb.Evidence{{Meta: &pb.RecordMeta{RecordId: "evidence:one"}}}}
	return &workflows.RAGResult{Evidence: evidence, Reranking: &retrieval.EvidenceRerankResult{Model: model, Evidence: proto.Clone(evidence).(*pb.EvidenceBundle), Batches: 1, Duration: time.Millisecond, Scores: []*pb.RerankResult{{PairId: "evidence:one", Score: 0.75, ModelManifest: proto.Clone(model).(*pb.ModelManifest), InputTokens: 8, Truncation: &pb.TruncationInfo{OriginalTokens: 8, RetainedTokens: 8}}}}}
}

func TestRerankingAggregateBudget(t *testing.T) {
	result := rerankBudgetFixture()
	model, _ := protojson.Marshal(result.Reranking.Model)
	score, _ := protojson.Marshal(result.Reranking.Scores[0])
	need := len(model) + len(score)
	if out, err := encodeRerankingBudget(result, true, need); err != nil || len(out.Scores) != 1 {
		t.Fatal(out, err)
	}
	for _, budget := range []int{0, len(model) - 1, need - 1} {
		if out, err := encodeRerankingBudget(result, true, budget); err == nil || out != nil || !strings.Contains(err.Error(), "byte budget") {
			t.Fatal("unbounded diagnostic retention", budget, out, err)
		}
	}
	result.Evidence.Items = append(result.Evidence.Items, &pb.Evidence{Meta: &pb.RecordMeta{RecordId: "evidence:two"}})
	result.Reranking.Evidence = proto.Clone(result.Evidence).(*pb.EvidenceBundle)
	second := proto.Clone(result.Reranking.Scores[0]).(*pb.RerankResult)
	second.PairId = "evidence:two"
	result.Reranking.Scores = append(result.Reranking.Scores, second)
	if out, err := encodeRerankingBudget(result, true, need); err == nil || out != nil || !strings.Contains(err.Error(), "byte budget") {
		t.Fatal("per-score budget did not accumulate", out, err)
	}
}
