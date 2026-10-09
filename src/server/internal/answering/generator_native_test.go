// Opt-in real llama.cpp generator/tokenizer test over synthetic trusted evidence.
// Verifies full-prompt usage parity, context packing and source-owned citations.
// GGUF bytes are hash-checked, including the embedded tokenizer; server startup
// provenance must be recorded externally. This is not legal/quality acceptance.
package answering

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
)

func TestNativeLlamaCitedDraft(t *testing.T) {
	if os.Getenv("REGULAGRAPH_TEST_LLAMA") != "1" {
		t.Skip("explicit local llama.cpp opt-in required")
	}
	endpoint := os.Getenv("REGULAGRAPH_TEST_LLAMA_ENDPOINT")
	u, err := url.Parse(endpoint)
	if err != nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Scheme != "http" {
		t.Fatal("literal loopback llama endpoint required")
	}
	modelID, version := os.Getenv("REGULAGRAPH_TEST_LLAMA_MODEL"), os.Getenv("REGULAGRAPH_TEST_LLAMA_BUILD")
	if modelID == "" || version == "" {
		t.Fatal("explicit model and build required")
	}
	f, err := os.Open(os.Getenv("REGULAGRAPH_TEST_LLAMA_GGUF"))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if digest != os.Getenv("REGULAGRAPH_TEST_LLAMA_SHA256") {
		t.Fatal("GGUF digest mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	provider, err := inference.NewOpenAICompatibleProvider(inference.OpenAICompatibleConfig{Endpoint: endpoint, APIKey: os.Getenv("REGULAGRAPH_TEST_LLAMA_KEY"), Timeout: 2 * time.Minute, MaximumResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	counter, err := inference.NewLlamaTokenCounter(provider, modelID, 1<<20, 16384)
	if err != nil {
		t.Fatal(err)
	}
	_, in := generatorFixture(t, provider)
	tokenizerHash := &pb.ContentHash{Sha256: digest}
	in.Context, err = BuildContext(ctx, in.Evidence, "context:native", tokenizerHash, 2000, 2, counter.CountText)
	if err != nil {
		t.Fatal(err)
	}
	model := &pb.ModelManifest{ModelId: modelID, Version: version, WeightsHash: tokenizerHash, TokenizerHash: tokenizerHash, Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 4096, Precision: "q4_k_m", Backend: "llama.cpp-cpu", PromptHash: DraftPromptHash()}
	producer := proto.Clone(in.Evidence.RetrievalManifest).(*pb.ProducerManifest)
	producer.Software = "regulagraph-native-generator-test"
	producer.Build = version
	producer.Models = []*pb.ModelManifest{model}
	producer.PromptHashes = []*pb.ContentHash{DraftPromptHash()}
	g, err := NewDraftGenerator(provider, counter.CountPrompt, DraftGeneratorConfig{Model: model, Producer: producer, MaximumInputBytes: 1 << 20, MaximumOutputBytes: 64 << 10, MaximumClaims: 8, MaximumCitations: 32, MaximumConcurrent: 1, OutputTokens: 256, AllowUnreviewedDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := g.Generate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.InputTokens == 0 || result.OutputTokens == 0 || len(result.Answer.Claims) == 0 || len(result.Answer.Citations) == 0 || result.Answer.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL {
		t.Fatalf("expected cited unreviewed draft: %+v", result)
	}
	for _, c := range result.Answer.Citations {
		if c.SourceUrl != "https://example.org/source.pdf" {
			t.Fatal("source authority lost")
		}
	}
	if err = ValidateGroundedAnswer(result.Answer, in.Context, in.Evidence, in.SourceURLs); err != nil {
		t.Fatal(err)
	}
	t.Logf("model=%s build=%s gguf=%s context_tokens=%d full_prompt_tokens=%d output_tokens=%d claims=%d citations=%d text=%q", modelID, version, digest, in.Context.TokenCount, result.InputTokens, result.OutputTokens, len(result.Answer.Claims), len(result.Answer.Citations), result.Answer.Text)
}
