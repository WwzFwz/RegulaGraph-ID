// Verifies CLI reranking diagnostics preserve model and evidence identity and
// cannot silently omit requested ranking. Synthetic scores test serialization
// and error handling only, not model quality or benchmark acceptance.
package main

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/workflows"
)

func queryRerankFixture() *workflows.RAGResult {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "reranker:fixture", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_RERANK, MaxTokens: 8192, Precision: "fp32", Backend: "fixture"}
	evidence := &pb.EvidenceBundle{Items: []*pb.Evidence{{Meta: &pb.RecordMeta{RecordId: "evidence:one"}}}}
	return &workflows.RAGResult{Evidence: evidence, Reranking: &retrieval.EvidenceRerankResult{Model: model, Evidence: proto.Clone(evidence).(*pb.EvidenceBundle), Batches: 1, Duration: time.Millisecond, Scores: []*pb.RerankResult{{PairId: "evidence:one", Score: 0.75, ModelManifest: proto.Clone(model).(*pb.ModelManifest), InputTokens: 8, Truncation: &pb.TruncationInfo{OriginalTokens: 8, RetainedTokens: 8}}}}}
}

func TestQueryRerankOutput(t *testing.T) {
	result := queryRerankFixture()
	out, err := queryRerankOutput(result, true)
	if err != nil {
		t.Fatal(err)
	}
	var score pb.RerankResult
	var model pb.ModelManifest
	if out.Batches != 1 || out.DurationNS != int64(time.Millisecond) || len(out.Scores) != 1 {
		t.Fatal("lost diagnostics")
	}
	if err = protojson.Unmarshal(out.Model, &model); err != nil || !proto.Equal(&model, result.Reranking.Model) {
		t.Fatal("model drift", err)
	}
	if err = protojson.Unmarshal(out.Scores[0], &score); err != nil || !proto.Equal(&score, result.Reranking.Scores[0]) {
		t.Fatal("score drift", err)
	}
	result.Reranking = nil
	if out, err = queryRerankOutput(result, false); err != nil || out != nil {
		t.Fatal("baseline requires ranking", err)
	}
}

func TestQueryRerankOutputRejectsDrift(t *testing.T) {
	for _, name := range []string{"missing", "unexpected", "count", "evidence", "identity", "model", "duration", "batches"} {
		t.Run(name, func(t *testing.T) {
			result := queryRerankFixture()
			required := true
			switch name {
			case "missing":
				result.Reranking = nil
			case "unexpected":
				required = false
			case "count":
				result.Reranking.Scores = nil
			case "evidence":
				result.Reranking.Evidence.Items[0].Text = "different"
			case "identity":
				result.Reranking.Scores[0].PairId = "other"
			case "model":
				result.Reranking.Scores[0].ModelManifest.Version = "other"
			case "duration":
				result.Reranking.Duration = -1
			case "batches":
				result.Reranking.Batches = -1
			}
			if out, err := queryRerankOutput(result, required); err == nil || out != nil {
				t.Fatal("accepted drift")
			}
		})
	}
}
