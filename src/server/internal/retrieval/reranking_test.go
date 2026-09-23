// Verifies that native reranker batches preserve evidence identity, score cardinality,
// truncation metadata, and deterministic ties before a result enters answer context.
// Fixture scores do not establish model quality or latency targets.
package retrieval

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func rerankFixture() ([]FusedCandidate, []string, *pb.RerankBatchResponse, *pb.ModelManifest) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "model:rerank", Version: "v1",
		WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK,
		MaxTokens: 512, Precision: "fp32", Backend: "fixture"}
	candidates := []FusedCandidate{{EvidenceKey: "evidence:b", BestRank: 1,
		Provenance: []*pb.Candidate{{EvidenceKey: "evidence:b"}}},
		{EvidenceKey: "evidence:a", BestRank: 2,
			Provenance: []*pb.Candidate{{EvidenceKey: "evidence:a"}}}}
	score := func(pair string, value float64) *pb.RerankItemResult {
		return &pb.RerankItemResult{PairId: pair,
			Result: &pb.RerankItemResult_Score{Score: &pb.RerankScore{
				Score: value, InputTokens: 10,
				Truncation: &pb.TruncationInfo{OriginalTokens: 10, RetainedTokens: 10}}}}
	}
	response := &pb.RerankBatchResponse{RequestId: "request:one", Model: proto.Clone(model).(*pb.ModelManifest),
		Results: []*pb.RerankItemResult{score("pair:a", 0.7), score("pair:b", 0.7)}}
	return candidates, []string{"pair:a", "pair:b"}, response, model
}

func TestCorrelateRerankBatchAcceptsReorderedResultsAndStableTies(t *testing.T) {
	candidates, ids, response, model := rerankFixture()
	response.Results[0], response.Results[1] = response.Results[1], response.Results[0]
	result, err := CorrelateRerankBatch(candidates, ids, response, "request:one", model, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Candidate.EvidenceKey != "evidence:b" ||
		result[1].Candidate.EvidenceKey != "evidence:a" || result[0].InputTokens != 10 {
		t.Fatalf("response order or tie affected evidence mapping: %+v", result)
	}
	response.Results[0].GetScore().Truncation.Truncated = true
	if result[0].Truncation.Truncated {
		t.Fatal("returned truncation aliases mutable response")
	}
	candidates[0].Provenance[0].EvidenceKey = "evidence:forged"
	if result[0].Candidate.Provenance[0].EvidenceKey != "evidence:b" {
		t.Fatal("returned provenance aliases mutable input")
	}
}

func TestCorrelateRerankBatchRejectsMissingDuplicateAndInvalidScores(t *testing.T) {
	candidates, ids, baseline, model := rerankFixture()
	for name, mutate := range map[string]func(*pb.RerankBatchResponse){
		"missing":       func(r *pb.RerankBatchResponse) { r.Results = r.Results[:1] },
		"duplicate":     func(r *pb.RerankBatchResponse) { r.Results[1].PairId = "pair:a" },
		"unknown":       func(r *pb.RerankBatchResponse) { r.Results[1].PairId = "pair:unknown" },
		"nonfinite":     func(r *pb.RerankBatchResponse) { r.Results[1].GetScore().Score = math.NaN() },
		"no truncation": func(r *pb.RerankBatchResponse) { r.Results[1].GetScore().Truncation = nil },
		"false truncation": func(r *pb.RerankBatchResponse) {
			r.Results[1].GetScore().Truncation.Truncated = true
		},
		"zero input tokens": func(r *pb.RerankBatchResponse) {
			r.Results[1].GetScore().InputTokens = 0
		},
		"model token limit": func(r *pb.RerankBatchResponse) {
			r.Results[1].GetScore().InputTokens = 513
		},
		"retained token limit": func(r *pb.RerankBatchResponse) {
			r.Results[1].GetScore().Truncation.OriginalTokens = 513
			r.Results[1].GetScore().Truncation.RetainedTokens = 513
		},
		"failed item": func(r *pb.RerankBatchResponse) {
			r.Results[1].Result = &pb.RerankItemResult_Error{Error: &pb.OperationError{
				Code: pb.ErrorCode_ERROR_CODE_UNAVAILABLE, SafeMessage: "model unavailable"}}
		},
		"wrong model": func(r *pb.RerankBatchResponse) { r.Model.Version = "v2" },
	} {
		t.Run(name, func(t *testing.T) {
			response := proto.Clone(baseline).(*pb.RerankBatchResponse)
			mutate(response)
			if _, err := CorrelateRerankBatch(candidates, ids, response, "request:one", model, 2); err == nil {
				t.Fatal("invalid rerank response accepted")
			}
		})
	}
	if _, err := CorrelateRerankBatch(candidates, ids, baseline, "request:other", model, 2); err == nil {
		t.Fatal("wrong request accepted")
	}
	if _, err := CorrelateRerankBatch(candidates, ids, baseline, "request:one", model, 1); err == nil {
		t.Fatal("budget exceeded without failure")
	}
}
