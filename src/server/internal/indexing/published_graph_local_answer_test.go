// Extends native graph publication through the real CLI and local model server.
// Config/manifest are explicit test pins, not an inferred production policy.
// Rust/PG/Neo4j/Qdrant and generator/tokenizer run; evidence/alias inputs remain
// synthetic, and abstention is a valid model decision, never a quality PASS.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/domain"
)

func checkPublishedGraphLocalAnswer(t *testing.T, ctx context.Context, binary string, env []string, index *domain.PinnedIndex, question string) {
	t.Helper()
	if os.Getenv("REGULAGRAPH_TEST_LLAMA_GRAPH_ANSWER") != "1" {
		return
	}
	path, configHash := nativeAnswerProfile(t, index.Pin.CorpusID)
	cmd := exec.CommandContext(ctx, binary, "query-evidence", "-answer", "-question", question, "-as-of", "2026-01-01", "-profile", "graph", "-limit", "8", "-timeout", "3m")
	cmd.Env = append(env, "REGULAGRAPH_ANSWER_CONFIG="+path, "REGULAGRAPH_ANSWER_CONFIG_SHA256="+configHash, "REGULAGRAPH_ANSWER_API_KEY="+os.Getenv("REGULAGRAPH_TEST_LLAMA_KEY"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal("actual graph answer CLI", err, stderr.String())
	}
	var result struct {
		Mode     string          `json:"mode"`
		Evidence json.RawMessage `json:"evidence"`
		Draft    struct {
			Answer       json.RawMessage `json:"answer"`
			InputTokens  uint64          `json:"input_tokens"`
			OutputTokens uint64          `json:"output_tokens"`
		} `json:"draft"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Mode != "answer_draft" {
		t.Fatal("answer envelope", err)
	}
	a := new(pb.Answer)
	b := new(pb.EvidenceBundle)
	if err := protojson.Unmarshal(result.Draft.Answer, a); err != nil {
		t.Fatal(err)
	}
	if err := protojson.Unmarshal(result.Evidence, b); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(a.Snapshot, index.Snapshot) || !proto.Equal(b.Snapshot, index.Snapshot) || result.Draft.InputTokens == 0 || result.Draft.OutputTokens == 0 {
		t.Fatal("native answer lost snapshot/accounting")
	}
	if a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL && a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN {
		t.Fatal("draft promoted to verified")
	}
	if a.SemanticStatus == pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL && len(a.Citations) == 0 {
		t.Fatal("draft lost citations")
	}
	t.Logf("actual graph answer CLI PASS: prompt=%d output=%d status=%s citations=%d text=%q", result.Draft.InputTokens, result.Draft.OutputTokens, a.SemanticStatus, len(a.Citations), a.Text)
}

func nativeAnswerProfile(t *testing.T, corpus string) (string, string) {
	t.Helper()
	hash := &pb.ContentHash{Sha256: os.Getenv("REGULAGRAPH_TEST_LLAMA_SHA256")}
	model := &pb.ModelManifest{ModelId: os.Getenv("REGULAGRAPH_TEST_LLAMA_MODEL"), Version: "gguf:" + hash.Sha256, WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 4096, Precision: "q4_k_m", Backend: "llama.cpp-cpu", PromptHash: answering.DraftPromptHash()}
	modelJSON, err := protojson.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"schema_version": 1, "corpus": corpus, "endpoint": os.Getenv("REGULAGRAPH_TEST_LLAMA_ENDPOINT"), "gguf_path": os.Getenv("REGULAGRAPH_TEST_LLAMA_GGUF"), "server_build": os.Getenv("REGULAGRAPH_TEST_LLAMA_BUILD"), "chat_template_sha256": os.Getenv("REGULAGRAPH_TEST_LLAMA_TEMPLATE_SHA256"), "model": json.RawMessage(modelJSON), "allow_unreviewed_drafts": true, "limits": map[string]any{"maximum_input_bytes": 1 << 20, "maximum_output_bytes": 64 << 10, "maximum_claims": 8, "maximum_citations": 32, "maximum_concurrent": 1, "maximum_evidence": 8, "output_tokens": 256, "maximum_context_tokens": 2048}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "answer.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path, fmt.Sprintf("%x", sha256.Sum256(raw))
}
