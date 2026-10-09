// Exercises operator query flags, pre-I/O validation, ProtoJSON evidence output,
// cancellation and redaction. Execution doubles test CLI behavior only; real
// catalog routing/hydration is exercised by the indexing integration suite.
// These tests do not prove model quality or required latency benchmarks.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

func queryEnvironment() map[string]string {
	return map[string]string{"REGULAGRAPH_QUERY_CORPUS_ID": "corpus:test", "REGULAGRAPH_BUILD_ID": "fixture", "REGULAGRAPH_POSTGRES_DSN": "secret-dsn", "REGULAGRAPH_ARTIFACTS_DIR": "fixture", "REGULAGRAPH_QUERY_NATIVE_ENDPOINT": "127.0.0.1:50053", "REGULAGRAPH_QDRANT_URL": "http://127.0.0.1:6333", "REGULAGRAPH_QDRANT_API_KEY": "secret-key"}
}

func TestQueryCurrentDateAdmissionAndOutput(t *testing.T) {
	for _, mode := range []string{"success", "no zone", "invalid zone", "both dates", "missing audit", "bad audit", "wrong zone"} {
		t.Run(mode, func(t *testing.T) {
			env := queryEnvironment()
			env["REGULAGRAPH_QUERY_TIME_ZONE"] = "Asia/Jakarta"
			if mode == "no zone" {
				delete(env, "REGULAGRAPH_QUERY_TIME_ZONE")
			}
			if mode == "invalid zone" {
				env["REGULAGRAPH_QUERY_TIME_ZONE"] = "Local"
			}
			args := []string{"-question", "izin sekarang?", "-current", "-profile", "vector"}
			if mode == "both dates" {
				args = append(args, "-as-of", "2026-01-01")
			}
			var out, stderr bytes.Buffer
			calls := 0
			code := runQueryEvidenceWith(context.Background(), args, &out, &stderr, func(k string) string { return env[k] }, func(_ context.Context, o queryOptions, r *pb.QuestionRequest) (*workflows.RAGResult, error) {
				calls++
				if !o.Current || o.TimeZone != "Asia/Jakarta" || r.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_CURRENT || r.TemporalScope.EffectiveAt != nil {
					t.Fatal("lost explicit current intent")
				}
				zone, _ := query.LoadQueryTimeZone(o.TimeZone)
				_, audit, err := query.ResolveTemporalScope(r.TemporalScope, zone, func() time.Time { return time.Date(2025, 12, 31, 17, 0, 0, 0, time.UTC) })
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing audit" {
					audit = nil
				}
				if mode == "bad audit" {
					audit.EffectiveDate.Year++
				}
				if mode == "wrong zone" {
					audit.TimeZone = "Asia/Bangkok"
				}
				hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
				return &workflows.RAGResult{Temporal: audit, Evidence: &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: o.Corpus, RecordId: "evidence:test"}, Snapshot: &pb.SnapshotRef{CorpusId: o.Corpus, SnapshotId: "snapshot:test", Sequence: 1, ManifestHash: hash, RepresentationGeneration: "generation:test"}, RetrievalManifest: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: hash}, Completeness: pb.Completeness_COMPLETENESS_NONE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}}, nil
			})
			want := 1
			if mode == "success" {
				want = 0
			}
			if mode == "no zone" || mode == "invalid zone" || mode == "both dates" {
				want = 2
				if calls != 0 {
					t.Fatal("invalid mode reached IO")
				}
			}
			if code != want {
				t.Fatal(code, stderr.String())
			}
			if mode == "success" {
				var result struct {
					Temporal *query.TemporalResolution `json:"temporal_resolution"`
				}
				if json.Unmarshal(out.Bytes(), &result) != nil || result.Temporal == nil || result.Temporal.EffectiveDate.Year != 2026 || result.Temporal.TimeZone != "Asia/Jakarta" {
					t.Fatal("lost date audit", out.String())
				}
			} else if out.Len() != 0 {
				t.Fatal("invalid output leaked")
			}
		})
	}
}

func TestQueryEvidenceNormalizationConfigurationAndTrace(t *testing.T) {
	for _, mode := range []query.NormalizationMode{query.OriginalQuestion, query.MechanicalQuestion} {
		t.Run(string(mode), func(t *testing.T) {
			env := queryEnvironment()
			env["REGULAGRAPH_QUERY_NORMALIZATION"] = string(mode)
			original := "  Pasal\t11 bukan 1 "
			var out, stderr bytes.Buffer
			code := runQueryEvidenceWith(context.Background(), []string{"-question", original, "-as-of", "2026-01-01", "-profile", "vector"}, &out, &stderr, func(k string) string { return env[k] }, func(_ context.Context, o queryOptions, r *pb.QuestionRequest) (*workflows.RAGResult, error) {
				if o.Normalization != mode || r.Question != original {
					t.Fatal("lost normalization policy or original question")
				}
				report, err := query.NormalizeQuestion(r.Question, o.Normalization)
				if err != nil {
					t.Fatal(err)
				}
				hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
				return &workflows.RAGResult{
					Search:   &workflows.CandidateSearchResult{Normalization: report},
					Evidence: &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: o.Corpus, RecordId: "evidence:test"}, Snapshot: &pb.SnapshotRef{CorpusId: o.Corpus, SnapshotId: "snapshot:test", Sequence: 1, ManifestHash: hash, RepresentationGeneration: "generation:test"}, RetrievalManifest: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: hash}, Completeness: pb.Completeness_COMPLETENESS_PARTIAL, MissingDependencies: []string{"parent:missing"}, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED},
				}, nil
			})
			var decoded struct {
				Normalization *query.NormalizedQuestion `json:"query_normalization"`
			}
			if code != 0 || json.Unmarshal(out.Bytes(), &decoded) != nil || decoded.Normalization == nil {
				t.Fatal(code, out.String(), stderr.String())
			}
			report := decoded.Normalization
			want := original
			if mode == query.MechanicalQuestion {
				want = "Pasal 11 bukan 1"
			}
			if report.Method != mode || report.Original != original || report.Search != want || (mode == query.MechanicalQuestion && len(report.Edits) == 0) {
				t.Fatal("incorrect CLI normalization trace", report)
			}
		})
	}
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
		{"unsupported normalization", nil, "REGULAGRAPH_QUERY_NORMALIZATION", "guess"},
		{"reranker path without pin", nil, "REGULAGRAPH_QUERY_RERANK_MANIFEST", "fixture.pb"},
		{"reranker pin without path", nil, "REGULAGRAPH_QUERY_RERANK_MANIFEST_SHA256", strings.Repeat("a", 64)},
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
