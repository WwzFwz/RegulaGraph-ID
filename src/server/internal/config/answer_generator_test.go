// Verifies pinned answer profile parsing before any backend I/O: complete C01
// model identity, explicit draft policy and bounded context/output budgets.
// Synthetic files cannot establish generator readiness or semantic quality.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/answering"
)

func answerConfigFixture(t *testing.T) map[string]any {
	t.Helper()
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "qwen", Version: "v1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 4096, Precision: "q4_k_m", Backend: "llama.cpp", PromptHash: answering.DraftPromptHash()}
	raw, err := protojson.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"schema_version": 1, "corpus": "corpus:test", "endpoint": "http://127.0.0.1:55101", "gguf_path": filepath.Join(t.TempDir(), "model.gguf"), "server_build": "b11515-3d65c90d0", "chat_template_sha256": strings.Repeat("b", 64), "model": json.RawMessage(raw), "allow_unreviewed_drafts": true, "limits": map[string]any{"maximum_input_bytes": 1 << 20, "maximum_output_bytes": 64 << 10, "maximum_claims": 8, "maximum_citations": 32, "maximum_concurrent": 2, "maximum_evidence": 64, "output_tokens": 256, "maximum_context_tokens": 2048}}
}
func writeAnswerConfig(t *testing.T, raw []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answer.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	return path, hex.EncodeToString(h[:])
}
func TestLoadAnswerGenerator(t *testing.T) {
	raw, _ := json.Marshal(answerConfigFixture(t))
	path, hash := writeAnswerConfig(t, raw)
	cfg, err := LoadAnswerGenerator(path, hash, "corpus:test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Binding.Model.ModelId != "qwen" || cfg.FileHash != hash || cfg.OutputTokens != 256 || cfg.MaximumEvidence != 64 {
		t.Fatal("configuration dropped fields")
	}
	if _, err = LoadAnswerGenerator(path, strings.Repeat("c", 64), "corpus:test"); err == nil {
		t.Fatal("hash mismatch accepted")
	}
	if _, err = LoadAnswerGenerator(path, hash, "corpus:other"); err == nil {
		t.Fatal("corpus mismatch accepted")
	}
}
func TestLoadAnswerGeneratorRejectsInvalidPolicy(t *testing.T) {
	for _, mode := range []string{"unknown", "missing", "draft-disabled", "draft-null", "remote", "url-user", "relative", "window", "output", "concurrency", "template", "model-task", "prompt", "null-model", "duplicate", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			data := answerConfigFixture(t)
			limits := data["limits"].(map[string]any)
			switch mode {
			case "unknown":
				data["api_key"] = "forbidden"
			case "missing":
				delete(limits, "maximum_evidence")
			case "draft-disabled":
				data["allow_unreviewed_drafts"] = false
			case "draft-null":
				data["allow_unreviewed_drafts"] = nil
			case "remote":
				data["endpoint"] = "https://example.org"
			case "url-user":
				data["endpoint"] = "http://secret@127.0.0.1:55101"
			case "relative":
				data["gguf_path"] = "model.gguf"
			case "window":
				limits["maximum_context_tokens"] = 4096
			case "output":
				limits["output_tokens"] = 4096
			case "concurrency":
				limits["maximum_concurrent"] = 0
			case "template":
				data["chat_template_sha256"] = "bad"
			case "null-model":
				data["model"] = nil
			case "model-task", "prompt":
				model := new(pb.ModelManifest)
				if err := protojson.Unmarshal(data["model"].(json.RawMessage), model); err != nil {
					t.Fatal(err)
				}
				if mode == "model-task" {
					model.Task = pb.ModelTask_MODEL_TASK_RESOLVE
				} else {
					model.PromptHash = &pb.ContentHash{Sha256: strings.Repeat("c", 64)}
				}
				raw, _ := protojson.Marshal(model)
				data["model"] = json.RawMessage(raw)
			}
			raw, _ := json.Marshal(data)
			if mode == "duplicate" {
				raw = []byte(strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1))
			}
			if mode == "oversize" {
				raw = append(raw, []byte(strings.Repeat(" ", 64<<10))...)
			}
			path, hash := writeAnswerConfig(t, raw)
			if _, err := LoadAnswerGenerator(path, hash, "corpus:test"); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
