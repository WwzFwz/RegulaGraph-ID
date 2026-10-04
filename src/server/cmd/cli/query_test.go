// Exercises operator query flags, pre-I/O validation, ProtoJSON evidence output,
// cancellation and redaction. Execution doubles test CLI behavior only; real
// catalog routing/hydration is exercised by the indexing integration suite.
// These tests do not prove model quality or required latency benchmarks.
package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/workflows"
)

func queryEnvironment() map[string]string {
	return map[string]string{"REGULAGRAPH_QUERY_CORPUS_ID": "corpus:test", "REGULAGRAPH_BUILD_ID": "fixture", "REGULAGRAPH_POSTGRES_DSN": "secret-dsn", "REGULAGRAPH_ARTIFACTS_DIR": "fixture", "REGULAGRAPH_QUERY_NATIVE_ENDPOINT": "127.0.0.1:50053", "REGULAGRAPH_QDRANT_URL": "http://127.0.0.1:6333", "REGULAGRAPH_QDRANT_API_KEY": "secret-key"}
}

func TestQueryEvidenceRejectsInvalidInputBeforeIO(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		key, value string
	}{
		{"missing profile", []string{"-question", "izin", "-as-of", "2026-01-01"}, "", ""},
		{"missing date", []string{"-question", "izin"}, "", ""},
		{"invalid date", []string{"-question", "izin", "-as-of", "2026-02-30"}, "", ""},
		{"unsupported profile", []string{"-question", "izin", "-as-of", "2026-01-01", "-profile", "graph"}, "", ""},
		{"excess candidates", []string{"-question", "izin", "-as-of", "2026-01-01", "-limit", "129"}, "", ""},
		{"remote plaintext native", nil, "REGULAGRAPH_QUERY_NATIVE_ENDPOINT", "192.0.2.1:50053"},
		{"remote plaintext qdrant", nil, "REGULAGRAPH_QDRANT_URL", "http://example.org"},
		{"credential URL", nil, "REGULAGRAPH_QDRANT_URL", "https://secret@example.org"},
		{"missing corpus", nil, "REGULAGRAPH_QUERY_CORPUS_ID", ""},
		{"invalid unresolved", []string{"-question", "izin", "-as-of", "2026-01-01", "-unresolved", "guess"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := queryEnvironment()
			if tc.key != "" {
				env[tc.key] = tc.value
			}
			args := tc.args
			if args == nil {
				args = []string{"-question", "izin", "-as-of", "2026-01-01"}
			}
			if tc.name != "missing profile" && !slices.Contains(args, "-profile") {
				args = append(args, "-profile", "vector")
			}
			var out, stderr bytes.Buffer
			code := runQueryEvidenceWith(context.Background(), args, &out, &stderr, func(k string) string { return env[k] }, func(context.Context, queryOptions, *pb.QuestionRequest) (*workflows.RAGResult, error) {
				t.Fatal("invalid request reached IO")
				return nil, nil
			})
			if code != 2 || out.Len() != 0 {
				t.Fatal(code, out.String())
			}
			if strings.Contains(stderr.String(), "secret") {
				t.Fatal("credential in error")
			}
		})
	}
}

func TestQueryEvidencePreservesPartialOutputAndRedactsFailures(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancelled", "invalid result", "foreign corpus", "failed completion"} {
		t.Run(mode, func(t *testing.T) {
			env := queryEnvironment()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out, stderr bytes.Buffer
			code := runQueryEvidenceWith(ctx, []string{"-question", "izin?", "-as-of", "2026-01-01", "-profile", "vector"}, &out, &stderr, func(k string) string { return env[k] }, func(c context.Context, o queryOptions, r *pb.QuestionRequest) (*workflows.RAGResult, error) {
				if _, ok := c.Deadline(); !ok {
					t.Fatal("missing execution deadline")
				}
				if r.CorpusId != "corpus:test" || r.RequestedProfile != pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG || r.Question != "izin?" {
					t.Fatal("query changed")
				}
				if mode == "failure" {
					return nil, errors.New("secret-key secret-dsn provider dump")
				}
				if mode == "cancelled" {
					cancel()
				}
				if mode == "invalid result" {
					return &workflows.RAGResult{}, nil
				}
				hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
				result := &workflows.RAGResult{Evidence: &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: o.Corpus, RecordId: "evidence:test"}, Snapshot: &pb.SnapshotRef{CorpusId: o.Corpus, SnapshotId: "snapshot:test", Sequence: 1, ManifestHash: hash, RepresentationGeneration: "generation:test"}, RetrievalManifest: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: hash}, Completeness: pb.Completeness_COMPLETENESS_PARTIAL, MissingDependencies: []string{"parent:missing"}, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}}
				if mode == "foreign corpus" {
					result.Evidence.Meta.CorpusId = "corpus:foreign"
					result.Evidence.Snapshot.CorpusId = "corpus:foreign"
				}
				if mode == "failed completion" {
					result.Evidence.CompletionStatus = pb.CompletionStatus_COMPLETION_STATUS_FAILED
				}
				return result, nil
			})
			if mode == "success" {
				if code != 0 || !strings.Contains(out.String(), "COMPLETENESS_PARTIAL") || !strings.Contains(out.String(), `"mode":"evidence"`) || strings.Contains(out.String(), `"answer"`) {
					t.Fatal(code, out.String(), stderr.String())
				}
			} else if code != 1 || out.Len() != 0 {
				t.Fatal(code, out.String())
			}
			if strings.Contains(stderr.String(), "secret") {
				t.Fatal("credential exposed")
			}
		})
	}
}
