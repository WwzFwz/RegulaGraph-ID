// Verifies semantic local prompt admission before generation and fail-closed
// usage/identity checks afterwards. HTTP/GGUF fixtures exercise control flow,
// not actual model quality, tokenizer accuracy or performance acceptance.
package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestSemanticLlamaPromptAdmission(t *testing.T) {
	for _, task := range []pb.ModelTask{pb.ModelTask_MODEL_TASK_EXTRACT, pb.ModelTask_MODEL_TASK_RESOLVE} {
		for _, mode := range []string{"boundary", "overflow", "missing_cap", "prompt_drift", "count_unavailable", "count_mismatch", "drift_after_count", "cancel"} {
			t.Run(task.String()+"/"+mode, func(t *testing.T) {
				calls, counted := 0, false
				p, binding := admissionFixture(t, func(props map[string]any) {
					if mode == "drift_after_count" && counted {
						props["chat_template"] = "changed"
					}
				}, func() { calls++ }, func(w http.ResponseWriter, r *http.Request) {
					counted = true
					if mode == "count_unavailable" {
						w.WriteHeader(503)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					var request chatRequest
					if err := json.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					if len(request.Messages) != 3 || request.Messages[1].Content != "trusted ontology" || request.ResponseFormat.JSONSchema.Name != "answer" || request.MaxTokens == 0 {
						t.Error("counter did not receive full generation envelope", string(body))
					}
					n := 99
					if mode == "count_mismatch" {
						n = 98
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"object": "response.input_tokens", "input_tokens": n})
				})
				defer p.Close()
				binding.Model.Task = task
				request := countRequest()
				request.SystemPrompt = "prompt"
				request.SystemContext = "trusted ontology"
				request.MaxOutputTokens = 3997 // 99 + 3997 exactly fills 4096.
				admitted, err := AdmitLlama(context.Background(), p, binding, 4096)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch mode {
				case "overflow":
					request.MaxOutputTokens++
				case "missing_cap":
					request.MaxOutputTokens = 0
				case "prompt_drift":
					request.SystemPrompt = "other"
				case "cancel":
					cancel()
				}
				result, err := admitted.Generate(ctx, request)
				if mode == "boundary" {
					if err != nil || result.InputTokens != 99 || calls != 1 {
						t.Fatalf("boundary failed: %+v %v calls=%d", result, err, calls)
					}
				} else {
					if err == nil || result.JSON != nil {
						t.Fatalf("invalid request/output accepted: %+v %v", result, err)
					}
					want := 0
					if mode == "count_mismatch" {
						want = 1
					}
					if calls != want {
						t.Fatalf("generation calls=%d want=%d", calls, want)
					}
				}
			})
		}
	}
}

func TestSemanticServiceRejectsDifferentAdmittedModel(t *testing.T) {
	fixture, _ := semanticFixture(&providerDouble{})
	local := &PinnedLlama{binding: LlamaModelBinding{Model: fixture.config.Model}}
	if _, err := NewSemanticService(local, fixture.config); err != nil {
		t.Fatal(err)
	}
	changed := fixture.config
	changed.Model = nil
	if _, err := NewSemanticService(local, changed); err == nil {
		t.Fatal("missing model accepted")
	}
	var missing *PinnedLlama
	if _, err := NewSemanticService(missing, fixture.config); err == nil {
		t.Fatal("nil local provider accepted")
	}
	local.binding.Model = &pb.ModelManifest{ModelId: "other"}
	if _, err := NewSemanticService(local, fixture.config); err == nil {
		t.Fatal("different admitted model accepted")
	}
}
