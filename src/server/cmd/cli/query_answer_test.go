// Tests explicit answer-mode admission and output without running databases or
// models. CLI must not fall back to evidence when generation fails or promote a
// draft to verified status. Native graph/model integration supplies separate proof.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

func queryAnswerEnvironment(t *testing.T) map[string]string {
	t.Helper()
	env := queryEnvironment()
	h := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	model := &pb.ModelManifest{ModelId: "qwen", Version: "v1", WeightsHash: h, TokenizerHash: h, Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 4096, Precision: "q4_k_m", Backend: "llama.cpp", PromptHash: answering.DraftPromptHash()}
	m, _ := protojson.Marshal(model)
	cfg := map[string]any{"schema_version": 1, "corpus": env["REGULAGRAPH_QUERY_CORPUS_ID"], "endpoint": "http://127.0.0.1:55101", "gguf_path": filepath.Join(t.TempDir(), "test.gguf"), "server_build": "test", "chat_template_sha256": h.Sha256, "model": json.RawMessage(m), "allow_unreviewed_drafts": true, "limits": map[string]any{"maximum_input_bytes": 1 << 20, "maximum_output_bytes": 64 << 10, "maximum_claims": 8, "maximum_citations": 32, "maximum_concurrent": 1, "maximum_evidence": 64, "output_tokens": 256, "maximum_context_tokens": 2048}}
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "answer.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	env["REGULAGRAPH_ANSWER_CONFIG"] = path
	env["REGULAGRAPH_ANSWER_CONFIG_SHA256"] = hex.EncodeToString(digest[:])
	env["REGULAGRAPH_ANSWER_API_KEY"] = "secret-local"
	return env
}
func TestQueryAnswerAdmissionAndNoFallback(t *testing.T) {
	for _, mode := range []string{"success", "missing config", "wrong hash", "too many candidates", "generation error", "missing draft", "foreign snapshot", "promoted status", "wrong date", "wrong date with audit", "current success", "current wrong date", "current missing draft"} {
		t.Run(mode, func(t *testing.T) {
			env := queryAnswerEnvironment(t)
			args := []string{"-answer", "-question", "izin?", "-as-of", "2026-01-01", "-profile", "vector"}
			if strings.HasPrefix(mode, "current ") {
				args = []string{"-answer", "-question", "izin?", "-current", "-profile", "vector"}
				env["REGULAGRAPH_QUERY_TIME_ZONE"] = "Asia/Jakarta"
			}
			switch mode {
			case "missing config":
				delete(env, "REGULAGRAPH_ANSWER_CONFIG")
			case "wrong hash":
				env["REGULAGRAPH_ANSWER_CONFIG_SHA256"] = strings.Repeat("b", 64)
			case "too many candidates":
				args = append(args, "-limit", "65")
			}
			var out, stderr bytes.Buffer
			called := false
			code := runQueryEvidenceWith(context.Background(), args, &out, &stderr, func(k string) string { return env[k] }, func(ctx context.Context, o queryOptions, q *pb.QuestionRequest) (*workflows.RAGResult, error) {
				called = true
				if !o.Answer || o.answerConfig == nil || o.answerKey != "secret-local" {
					t.Fatal("answer config lost")
				}
				if mode == "generation error" {
					return nil, errors.New("secret provider details")
				}
				h := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
				snapshot := &pb.SnapshotRef{CorpusId: o.Corpus, SnapshotId: "snapshot:test", Sequence: 1, ManifestHash: h, RepresentationGeneration: "generation:test"}
				producer := &pb.ProducerManifest{Software: "test", Build: "test", SchemaVersion: 1, ConfigHash: h}
				e := &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: o.Corpus, RecordId: "evidence:test"}, Snapshot: snapshot, RetrievalManifest: producer, Completeness: pb.Completeness_COMPLETENESS_NONE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
				zone, _ := query.LoadQueryTimeZone(o.TimeZone)
				resolved, audit, err := query.ResolveTemporalScope(q.TemporalScope, zone, func() time.Time { return time.Date(2025, 12, 31, 17, 0, 0, 0, time.UTC) })
				if err != nil {
					t.Fatal(err)
				}
				a := &pb.Answer{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: o.Corpus, RecordId: "answer:test"}, RequestId: "request:test", Text: answering.AbstainText, Snapshot: proto.Clone(snapshot).(*pb.SnapshotRef), EffectiveDates: []*pb.CalendarDate{resolved.EffectiveAt}, RunManifest: producer, SemanticStatus: pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
				r := &workflows.RAGResult{Evidence: e, Answer: &workflows.EvidenceAnswerResult{Draft: &answering.DraftResult{Answer: a}}}
				if o.Current || mode == "wrong date with audit" {
					r.Temporal = audit
				}
				if strings.Contains(mode, "wrong date") {
					a.EffectiveDates = []*pb.CalendarDate{{Year: 2027, Month: 1, Day: 1}}
				}
				if mode == "missing draft" || mode == "current missing draft" {
					r.Answer = nil
				}
				if mode == "foreign snapshot" {
					a.Snapshot.SnapshotId = "snapshot:other"
				}
				if mode == "promoted status" {
					a.SemanticStatus = pb.SemanticStatus_SEMANTIC_STATUS_COMPLETE
				}
				return r, nil
			})
			preflight := mode == "missing config" || mode == "wrong hash" || mode == "too many candidates"
			if preflight {
				if called || code != 2 || out.Len() != 0 {
					t.Fatal("invalid profile reached IO", code, out.String())
				}
			} else if mode == "success" || mode == "current success" {
				if code != 0 || !strings.Contains(out.String(), `"mode":"answer_draft"`) || !strings.Contains(out.String(), "SEMANTIC_STATUS_ABSTAIN") {
					t.Fatal(code, out.String(), stderr.String())
				}
			} else if code != 1 || out.Len() != 0 {
				t.Fatal("generation failure fell back or emitted output", code, out.String())
			}
			if strings.Contains(stderr.String(), "secret") {
				t.Fatal("provider details leaked")
			}
		})
	}
}
