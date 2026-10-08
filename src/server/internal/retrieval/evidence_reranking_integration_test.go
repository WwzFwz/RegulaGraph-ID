// Exercises evidence batching through the actual native gRPC client/model when
// explicitly configured. Evidence text is synthetic; this proves transport and
// identity/order preservation, not gold relevance or required latency targets.
package retrieval

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/config"
)

func TestEvidenceRerankingNativeIntegration(t *testing.T) {
	endpoint := os.Getenv("REGULAGRAPH_TEST_NATIVE_ENDPOINT")
	if endpoint == "" {
		t.Skip("native endpoint and model manifest required")
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("native test endpoint must be loopback")
	}
	path := os.Getenv("REGULAGRAPH_TEST_NATIVE_RERANK_MANIFEST")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := fmt.Sprintf("%x", sha256.Sum256(raw))
	model, err := config.LoadNativeModel(path, manifestHash, pb.ModelTask_MODEL_TASK_RERANK)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client, err := inference.NewNativeClient(connection, model)
	if err != nil {
		t.Fatal(err)
	}
	call, bundle, cfg := evidenceRerankFixture()
	ctx, cancel := context.WithDeadline(context.Background(), call.Deadline.AsTime())
	defer cancel()
	if _, err := client.GetCapabilities(ctx, &pb.CapabilitiesRequest{Context: call}); err != nil {
		t.Fatal(err)
	}
	cfg.Model = model
	ranker, err := NewEvidenceReranker(client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	original := proto.Clone(bundle)
	result, err := ranker.Rank(ctx, call, "Apa ketentuan izin usaha?", bundle)
	if err != nil {
		t.Fatal(err)
	}
	if result.Batches != 2 || len(result.Scores) != len(bundle.Items) || !proto.Equal(bundle, original) {
		t.Fatal("batch/accounting/ownership drift")
	}
	for i, score := range result.Scores {
		if score.PairId != result.Evidence.Items[i].Meta.RecordId || !proto.Equal(score.ModelManifest, model) || score.InputTokens == 0 || score.Truncation.Truncated {
			t.Fatal("invalid native score metadata")
		}
		if i > 0 && score.Score > result.Scores[i-1].Score {
			t.Fatal("native score order drift")
		}
		t.Logf("pair=%s score=%g input_tokens=%d", score.PairId, score.Score, score.InputTokens)
	}
	t.Logf("model=%s revision=%s manifest_sha256=%s batches=%d diagnostic_duration=%s", model.ModelId, model.Version, manifestHash, result.Batches, result.Duration)
}
