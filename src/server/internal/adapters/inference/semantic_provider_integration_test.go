// Opt-in smoke test of a real structured-output endpoint through the production HTTP adapter.
// Synthetic contextual input and the repository prompt/schema verify protocol, token usage and
// output projection only. This does not pin/approve a production model or measure gold accuracy,
// tokenizer parity, throughput or required latency. Missing endpoint/model means SKIP, not PASS.
package inference

import (
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
)

func TestStructuredProviderEndpointIntegration(t *testing.T) {
	endpoint, model := os.Getenv("REGULAGRAPH_TEST_SEMANTIC_ENDPOINT"), os.Getenv("REGULAGRAPH_TEST_SEMANTIC_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("explicit semantic endpoint and model are required")
	}
	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Endpoint: endpoint,
		APIKey: os.Getenv("REGULAGRAPH_TEST_SEMANTIC_API_KEY"), Timeout: 90 * time.Second, MaximumResponseBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	fixture, request := resolutionFixture(provider)
	input, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(request.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	started := time.Now()
	response, err := provider.Generate(ctx, StructuredRequest{ModelID: model, SystemPrompt: fixture.config.SystemPrompt,
		ItemID: request.Items[0].ItemId, Text: string(input), SchemaName: fixture.config.SchemaName, Schema: fixture.config.OutputSchema})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := projectResolution(request, request.Items[0], response.JSON)
	if err != nil {
		t.Fatalf("real provider output failed projection: %v", err)
	}
	if response.InputTokens == 0 || response.OutputTokens == 0 {
		t.Fatal("provider omitted actual token usage")
	}
	t.Logf("protocol smoke: model=%s elapsed=%s input_tokens=%d output_tokens=%d action=%s; not an acceptance benchmark",
		model, time.Since(started), response.InputTokens, response.OutputTokens, proposal.Action)
}
