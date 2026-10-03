// Exercises structured generation through the real HTTP provider adapter and
// production context/citation/final validation. Synthetic model output tests
// structural grounding only; semantic quality and token parity are unmeasured.
package answering

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
)

type draftProviderFunc func(context.Context, inference.StructuredRequest) (inference.StructuredResponse, error)

func (f draftProviderFunc) Generate(ctx context.Context, r inference.StructuredRequest) (inference.StructuredResponse, error) {
	return f(ctx, r)
}

func generatorFixture(t *testing.T, provider inference.StructuredProvider) (*DraftGenerator, DraftInput) {
	t.Helper()
	_, rendered, bundle := groundedFixture(t)
	model := &pb.ModelManifest{ModelId: "generator:test", Version: "1", WeightsHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)}, TokenizerHash: rendered.TokenizerHash,
		Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 4096, Precision: "fp16", Backend: "test", PromptHash: DraftPromptHash()}
	producer := proto.Clone(bundle.RetrievalManifest).(*pb.ProducerManifest)
	producer.Models = []*pb.ModelManifest{model}
	producer.PromptHashes = []*pb.ContentHash{DraftPromptHash()}
	g, err := NewDraftGenerator(provider, func(context.Context, inference.StructuredRequest) (uint64, error) { return 100, nil }, DraftGeneratorConfig{Model: model, Producer: producer, MaximumInputBytes: 1 << 20, MaximumOutputBytes: 64 << 10, MaximumClaims: 8, MaximumCitations: 32, MaximumConcurrent: 2, OutputTokens: 512, AllowUnreviewedDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	return g, DraftInput{RequestID: "request:one", RecordID: "answer:one", Question: "Apa isi pasal pertama?", Context: rendered, Evidence: bundle, SourceURLs: trustedURL, EffectiveDates: []*pb.CalendarDate{{Year: 2026, Month: 1, Day: 1}}}
}

func TestDraftGeneratorHTTPProducesUnreviewedCitedSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("wrong provider path")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["max_tokens"]) != "512" || !strings.Contains(string(body["messages"]), "Apa isi pasal pertama?") {
			t.Error("output budget/question lost")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "generator:test", "choices": []any{map[string]any{"message": map[string]string{"content": `{"status":"answer","claims":[{"text":"Isi pasal pertama — café.","evidence_ids":["evidence:one"]},{"text":"Isi kedua.","evidence_ids":["evidence:two"]}]}`}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 30}})
	}))
	defer server.Close()
	provider, err := inference.NewOpenAICompatibleProvider(inference.OpenAICompatibleConfig{Endpoint: server.URL, Timeout: time.Second, MaximumResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	g, in := generatorFixture(t, provider)
	result, err := g.Generate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	a := result.Answer
	if a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL || len(a.Citations) != 2 || a.Claims[0].SupportStatus != pb.SupportStatus_SUPPORT_STATUS_UNREVIEWED {
		t.Fatal("draft promoted to verified or lost citations")
	}
	if a.Claims[1].AnswerTextSpan.StartByte != uint64(len("Isi pasal pertama — café.\n")) {
		t.Fatal("offsets are not UTF-8 bytes")
	}
	if a.Citations[0].SourceUrl != "https://example.org/source.pdf" {
		t.Fatal("citation URL not from trusted metadata")
	}
	if err := ValidateGroundedAnswer(a, in.Context, in.Evidence, in.SourceURLs); err != nil {
		t.Fatal(err)
	}
}

func TestDraftGeneratorRejectsProviderAndContextDrift(t *testing.T) {
	valid := `{"status":"answer","claims":[{"text":"Isi pasal pertama.","evidence_ids":["evidence:one"]}]}`
	for name, raw := range map[string]string{
		"unknown evidence":    strings.Replace(valid, "evidence:one", "invented", 1),
		"extra prose":         strings.Replace(valid, `"status":`, `"free_text":"invented", "status":`, 1),
		"unsupported status":  strings.Replace(valid, `"answer"`, `"complete"`, 1),
		"empty claims":        `{"status":"answer","claims":[]}`,
		"claims with abstain": strings.Replace(valid, `"answer"`, `"abstain"`, 1),
		"duplicate evidence":  strings.Replace(valid, `["evidence:one"]`, `["evidence:one","evidence:one"]`, 1),
		"trailing":            valid + ` {}`,
		"duplicate key":       strings.Replace(valid, `"text":`, `"text":"hidden", "text":`, 1),
		"excessive nesting":   `{"status":"answer","claims":[{"text":"x","evidence_ids":[[["evidence:one"]]]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			g, in := generatorFixture(t, draftProviderFunc(func(context.Context, inference.StructuredRequest) (inference.StructuredResponse, error) {
				return inference.StructuredResponse{JSON: json.RawMessage(raw), InputTokens: 100, OutputTokens: 10}, nil
			}))
			if _, err := g.Generate(context.Background(), in); err == nil {
				t.Fatal("invalid draft accepted")
			}
		})
	}
	for _, mutation := range []string{"context", "dates", "tokenizer", "budget", "usage", "zero output", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			called := false
			g, in := generatorFixture(t, draftProviderFunc(func(context.Context, inference.StructuredRequest) (inference.StructuredResponse, error) {
				called = true
				if mutation == "zero output" {
					return inference.StructuredResponse{JSON: json.RawMessage(valid), InputTokens: 100}, nil
				}
				return inference.StructuredResponse{JSON: json.RawMessage(valid), InputTokens: 99, OutputTokens: 10}, nil
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mutation {
			case "context":
				in.Context.RenderedBlocks[0].RenderedText = "forged"
			case "dates":
				in.EffectiveDates = nil
			case "tokenizer":
				in.Context.TokenizerHash = &pb.ContentHash{Sha256: strings.Repeat("c", 64)}
			case "budget":
				g.count = func(context.Context, inference.StructuredRequest) (uint64, error) { return 4096, nil }
			case "cancel":
				cancel()
			}
			if _, err := g.Generate(ctx, in); err == nil {
				t.Fatal("invalid input/usage accepted")
			}
			if mutation != "usage" && mutation != "zero output" && called {
				t.Fatal("invalid input reached provider")
			}
		})
	}
}

func TestDraftGeneratorRecordsExactRequestWithoutMutatingProducer(t *testing.T) {
	var sent inference.StructuredRequest
	g, in := generatorFixture(t, draftProviderFunc(func(_ context.Context, r inference.StructuredRequest) (inference.StructuredResponse, error) {
		sent = r
		return inference.StructuredResponse{JSON: json.RawMessage(`{"status":"abstain","claims":[]}`), InputTokens: 100, OutputTokens: 10}, nil
	}))
	before := proto.Clone(g.config.Producer).(*pb.ProducerManifest)
	for i := 0; i < 2; i++ {
		result, err := g.Generate(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		hashes := result.Answer.RunManifest.InputHashes
		if len(hashes) != len(before.InputHashes)+2 {
			t.Fatal("request audit hashes missing or accumulated across calls")
		}
		for j, raw := range [][]byte{[]byte(sent.Text), sent.Schema} {
			digest := sha256.Sum256(raw)
			if hashes[len(before.InputHashes)+j].Sha256 != hex.EncodeToString(digest[:]) {
				t.Fatal("audit hash differs from actual provider request")
			}
		}
		if !proto.Equal(before, g.config.Producer) {
			t.Fatal("shared pinned producer mutated")
		}
	}
}

func TestDraftGeneratorAdmissionCoversPreflight(t *testing.T) {
	entered := make(chan struct{})
	provider := draftProviderFunc(func(ctx context.Context, _ inference.StructuredRequest) (inference.StructuredResponse, error) {
		close(entered)
		<-ctx.Done()
		return inference.StructuredResponse{}, ctx.Err()
	})
	prototype, in := generatorFixture(t, provider)
	config := prototype.config
	config.MaximumConcurrent = 1
	g, err := NewDraftGenerator(provider, prototype.count, config)
	if err != nil {
		t.Fatal(err)
	}
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { defer close(finished); _, _ = g.Generate(first, in) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first request did not enter provider")
	}
	second := in
	second.Context = proto.Clone(in.Context).(*pb.ContextBundle)
	second.Context.RenderedBlocks[0].RenderedText = "bad preflight"
	wait, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err = g.Generate(wait, second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("preflight ran outside admission limit", err)
	}
	cancel()
	<-finished
}

func TestDraftGeneratorEmptyEvidenceAbstainsWithoutModel(t *testing.T) {
	g, in := generatorFixture(t, draftProviderFunc(func(context.Context, inference.StructuredRequest) (inference.StructuredResponse, error) {
		t.Fatal("empty evidence called provider")
		return inference.StructuredResponse{}, nil
	}))
	in.Evidence.Items = nil
	in.Evidence.Completeness = pb.Completeness_COMPLETENESS_NONE
	var err error
	in.Context, err = BuildContext(context.Background(), in.Evidence, "context:empty", g.config.Model.TokenizerHash, 1000, 2, countBytes)
	if err != nil {
		t.Fatal(err)
	}
	result, err := g.Generate(context.Background(), in)
	if err != nil || result.Answer.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN || result.Answer.Text != AbstainText {
		t.Fatal("empty evidence did not abstain", err)
	}
}
