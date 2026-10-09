// Exercises pinned local model admission with fake GGUF bytes and HTTP metadata.
// Drift before/after generation must suppress output; fixtures do not prove real
// GGUF validity or model quality. A separate native test uses the actual container.
package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func admissionHash(value string) *pb.ContentHash {
	h := sha256.Sum256([]byte(value))
	return &pb.ContentHash{Sha256: hex.EncodeToString(h[:])}
}
func admissionFixture(t *testing.T, mutate func(map[string]any), generate func()) (*OpenAICompatibleProvider, LlamaModelBinding) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("GGUF fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	b := LlamaModelBinding{GGUFPath: path, ServerBuild: "test-build", TemplateHash: admissionHash("template"), Model: &pb.ModelManifest{ModelId: "pinned", Version: "weights-v1", WeightsHash: admissionHash("GGUF fixture"), TokenizerHash: admissionHash("GGUF fixture"), Task: pb.ModelTask_MODEL_TASK_GENERATE, PromptHash: admissionHash("prompt"), MaxTokens: 4096, Precision: "q4_k_m", Backend: "llama.cpp"}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("missing metadata auth")
		}
		switch r.URL.Path {
		case "/props":
			fields := map[string]any{"model_alias": "pinned", "model_path": path, "build_info": "test-build", "chat_template": "template", "is_sleeping": false, "endpoint_props": false, "default_generation_settings": map[string]any{"n_ctx": 4096}, "future_field": "allowed"}
			if mutate != nil {
				mutate(fields)
			}
			_ = json.NewEncoder(w).Encode(fields)
		case "/v1/chat/completions":
			if generate != nil {
				generate()
			}
			_, _ = w.Write([]byte(`{"model":"pinned","choices":[{"message":{"content":"{}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":99,"completion_tokens":2}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	p, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Endpoint: s.URL, APIKey: "test-only", Timeout: time.Second, MaximumResponseBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	return p, b
}
func TestLlamaAdmissionOwnsPinsAndRejectsMetadataDrift(t *testing.T) {
	var drift string
	p, b := admissionFixture(t, func(fields map[string]any) {
		switch drift {
		case "alias":
			fields["model_alias"] = "other"
		case "path":
			fields["model_path"] = "other.gguf"
		case "build":
			fields["build_info"] = "other"
		case "template":
			fields["chat_template"] = "other"
		case "ctx":
			fields["default_generation_settings"] = map[string]any{"n_ctx": 2048}
		case "sleep":
			fields["is_sleeping"] = true
		case "mutable":
			fields["endpoint_props"] = true
		case "missing":
			delete(fields, "endpoint_props")
		case "null":
			fields["is_sleeping"] = nil
		case "case":
			fields["MODEL_ALIAS"] = "pinned"
		}
	}, nil)
	admitted, err := AdmitLlama(context.Background(), p, b, 4096)
	if err != nil {
		t.Fatal(err)
	}
	b.Model.ModelId = "mutated"
	b.TemplateHash.Sha256 = strings.Repeat("f", 64)
	if err = admitted.Ready(context.Background()); err != nil {
		t.Fatal("caller changed internal pins", err)
	}
	for _, drift = range []string{"alias", "path", "build", "template", "ctx", "sleep", "mutable", "missing", "null", "case"} {
		if err = admitted.Ready(context.Background()); err == nil {
			t.Fatal("metadata drift accepted", drift)
		}
	}
	drift = ""
	if err = os.WriteFile(admitted.binding.GGUFPath, []byte("GGUF changed length"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = admitted.Ready(context.Background()); err == nil {
		t.Fatal("file drift accepted")
	}
}
func TestLlamaAdmissionRejectsDriftAfterGeneration(t *testing.T) {
	changed := false
	p, b := admissionFixture(t, func(fields map[string]any) {
		if changed {
			fields["chat_template"] = "changed"
		}
	}, func() { changed = true })
	admitted, err := AdmitLlama(context.Background(), p, b, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := admitted.Generate(context.Background(), countRequest()); err == nil || result.JSON != nil {
		t.Fatal("drifted generation escaped", err)
	}
}
func TestLlamaAdmissionRejectsInvalidContainerAndCancellation(t *testing.T) {
	for _, mode := range []string{"hash", "magic", "tokenizer", "relative", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			p, b := admissionFixture(t, nil, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "hash":
				b.Model.WeightsHash = admissionHash("GGUF other")
				b.Model.TokenizerHash = b.Model.WeightsHash
			case "magic":
				_ = os.WriteFile(b.GGUFPath, []byte("wrong magic"), 0600)
				b.Model.WeightsHash = admissionHash("wrong magic")
				b.Model.TokenizerHash = b.Model.WeightsHash
			case "tokenizer":
				b.Model.TokenizerHash = admissionHash("other")
			case "relative":
				b.GGUFPath = "model.gguf"
			case "cancel":
				cancel()
			}
			if _, err := AdmitLlama(ctx, p, b, 4096); err == nil {
				t.Fatal("invalid admission accepted")
			}
		})
	}
}
func TestLlamaPropertyObjectRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{`{"model_alias":"a","model_alias":"b"}`, `{"model_alias":"a","MODEL_ALIAS":"b"}`, `null`, `{} {}`, `[]`} {
		if _, err := llamaPropertyObject([]byte(raw)); err == nil {
			t.Fatal("ambiguous props accepted", raw)
		}
	}
}
