// Verifies comparison flags before I/O and validates dated output at the CLI.
// Synthetic bundles test transport/error boundaries; production snapshot reuse
// is tested in workflows. No model-quality or performance acceptance is claimed.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

func cliComparisonFixture(t *testing.T, r *pb.QuestionRequest) *workflows.RAGResult {
	t.Helper()
	scopes, err := query.PlanComparisonScopes(r.TemporalScope)
	if err != nil {
		t.Fatal(err)
	}
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	snapshot := &pb.SnapshotRef{CorpusId: r.CorpusId, SnapshotId: "snapshot:test", Sequence: 1, RepresentationGeneration: "generation:test", ManifestHash: hash}
	c := &workflows.RAGComparisonResult{Snapshot: snapshot, Profile: r.RequestedProfile}
	for i, scope := range scopes {
		_, audit, err := query.ResolveTemporalScope(scope, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		b := &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: r.CorpusId, RecordId: fmt.Sprintf("bundle:date-%d", i)}, Snapshot: proto.Clone(snapshot).(*pb.SnapshotRef), RetrievalManifest: &pb.ProducerManifest{SchemaVersion: 1, Software: "fixture", Build: "test", ConfigHash: hash}, Completeness: pb.Completeness_COMPLETENESS_NONE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
		c.Dates = append(c.Dates, workflows.DatedEvidence{Date: scope.EffectiveAt, Result: &workflows.RAGResult{Evidence: b, Temporal: audit, Search: &workflows.CandidateSearchResult{Profile: r.RequestedProfile, Snapshot: snapshot, Normalization: &query.NormalizedQuestion{Original: r.Question}}}})
	}
	return &workflows.RAGResult{Comparison: c}
}

func TestCLIComparison(t *testing.T) {
	for _, mode := range []string{"success", "duplicate", "invalid date", "one date", "too many", "as-of conflict", "current conflict", "answer conflict", "wrong date", "missing rerank", "cancelled", "write failure", "execution failure"} {
		t.Run(mode, func(t *testing.T) {
			env := queryEnvironment()
			args := []string{"-question", "izin?", "-profile", "vector", "-compare-dates", "2026-01-01,2025-01-01"}
			switch mode {
			case "duplicate":
				args[len(args)-1] = "2026-01-01,2026-01-01"
			case "invalid date":
				args[len(args)-1] = "2026-02-30,2025-01-01"
			case "one date":
				args[len(args)-1] = "2026-01-01"
			case "too many":
				args[len(args)-1] = "2026-01-01,2025-01-01,2024-01-01,2023-01-01,2022-01-01,2021-01-01,2020-01-01,2019-01-01,2018-01-01"
			case "as-of conflict":
				args = append(args, "-as-of", "2026-01-01")
			case "current conflict":
				args = append(args, "-current")
				env["REGULAGRAPH_QUERY_TIME_ZONE"] = "UTC"
			case "answer conflict":
				args = append(args, "-answer")
			case "missing rerank":
				env["REGULAGRAPH_QUERY_RERANK_MANIFEST"] = "fixture"
				env["REGULAGRAPH_QUERY_RERANK_MANIFEST_SHA256"] = strings.Repeat("a", 64)
			}
			var out, stderr bytes.Buffer
			writer := comparisonWriter{buffer: &out, fail: mode == "write failure"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			code := runQueryEvidenceWith(ctx, args, writer, &stderr, func(k string) string { return env[k] }, func(_ context.Context, _ queryOptions, r *pb.QuestionRequest) (*workflows.RAGResult, error) {
				calls++
				if mode == "execution failure" {
					return nil, errors.New("secret-backend-error")
				}
				result := cliComparisonFixture(t, r)
				if mode == "wrong date" {
					result.Comparison.Dates[1].Date = &pb.CalendarDate{Year: 2024, Month: 1, Day: 1}
				}
				if mode == "cancelled" {
					cancel()
				}
				return result, nil
			})
			want := 2
			wantCalls := 0
			switch mode {
			case "success":
				want = 0
				wantCalls = 1
			case "wrong date", "missing rerank", "cancelled", "write failure", "execution failure":
				want = 1
				wantCalls = 1
			}
			if code != want || calls != wantCalls {
				t.Fatal(code, calls, stderr.String())
			}
			if mode == "success" {
				var obj struct {
					Mode  string
					Dates []json.RawMessage
				}
				if json.Unmarshal(out.Bytes(), &obj) != nil || obj.Mode != "evidence_comparison" || len(obj.Dates) != 2 {
					t.Fatal(out.String())
				}
			} else if out.Len() != 0 {
				t.Fatal("failure leaked evidence")
			}
			if strings.Contains(stderr.String(), "secret-backend-error") {
				t.Fatal("secret leak")
			}
		})
	}
}

type comparisonWriter struct {
	buffer *bytes.Buffer
	fail   bool
}

func (w comparisonWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, errors.New("write failed")
	}
	return w.buffer.Write(p)
}
