// Exercises public comparison admission and atomic, date-bound C01 serialization.
// Synthetic evidence tests identity/cancellation/error boundaries, not legal or
// model quality. Single-date regressions live in evidence/temporal tests.
package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/api/schemas"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

type comparisonService struct {
	calls  int
	mutate func(*workflows.RAGResult)
	cancel context.CancelFunc
}

func (*comparisonService) Ready(context.Context) error { return nil }
func (*comparisonService) AnswerEnabled() bool         { return true }
func (s *comparisonService) Answer(context.Context, *pb.QuestionRequest) (*workflows.RAGResult, error) {
	s.calls++
	return nil, fmt.Errorf("unexpected generation")
}
func (s *comparisonService) Search(_ context.Context, r *pb.QuestionRequest) (*workflows.RAGResult, error) {
	s.calls++
	plans, err := query.PlanComparisonScopes(r.TemporalScope)
	if err != nil {
		return nil, err
	}
	cmp := &workflows.RAGComparisonResult{Profile: r.RequestedProfile, Snapshot: bundleFixture().Snapshot}
	for i, scope := range plans {
		bundle := bundleFixture()
		bundle.Meta.RecordId = fmt.Sprintf("bundle:date-%d", i)
		_, audit, err := query.ResolveTemporalScope(scope, nil, nil)
		if err != nil {
			return nil, err
		}
		cmp.Dates = append(cmp.Dates, workflows.DatedEvidence{Date: proto.Clone(scope.EffectiveAt).(*pb.CalendarDate), Result: &workflows.RAGResult{Evidence: bundle, Temporal: audit, Search: &workflows.CandidateSearchResult{Profile: r.RequestedProfile, Snapshot: proto.Clone(cmp.Snapshot).(*pb.SnapshotRef), Normalization: &query.NormalizedQuestion{Original: r.Question}}}})
	}
	run := &workflows.RAGResult{Comparison: cmp}
	if s.mutate != nil {
		s.mutate(run)
	}
	if s.cancel != nil {
		s.cancel()
	}
	return run, nil
}

func TestComparisonHTTP(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*workflows.RAGResult)
	}{
		{"success", nil},
		{"missing date", func(r *workflows.RAGResult) { r.Comparison.Dates = r.Comparison.Dates[:1] }},
		{"reordered dates", func(r *workflows.RAGResult) {
			r.Comparison.Dates[0], r.Comparison.Dates[1] = r.Comparison.Dates[1], r.Comparison.Dates[0]
		}},
		{"snapshot drift", func(r *workflows.RAGResult) { r.Comparison.Dates[1].Result.Evidence.Snapshot.Sequence++ }},
		{"profile drift", func(r *workflows.RAGResult) { r.Comparison.Profile = pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG }},
		{"duplicate bundle", func(r *workflows.RAGResult) {
			r.Comparison.Dates[1].Result.Evidence.Meta.RecordId = r.Comparison.Dates[0].Result.Evidence.Meta.RecordId
		}},
		{"wrong audit", func(r *workflows.RAGResult) { r.Comparison.Dates[1].Result.Temporal.EffectiveDate.Year++ }},
		{"missing audit", func(r *workflows.RAGResult) { r.Comparison.Dates[1].Result.Temporal = nil }},
		{"wrong question", func(r *workflows.RAGResult) {
			r.Comparison.Dates[1].Result.Search.Normalization.Original = "other question"
		}},
		{"failed bucket", func(r *workflows.RAGResult) {
			r.Comparison.Dates[1].Result.Evidence.CompletionStatus = pb.CompletionStatus_COMPLETION_STATUS_FAILED
		}},
		{"mixed output", func(r *workflows.RAGResult) { r.Evidence = bundleFixture() }},
		{"nil snapshot", func(r *workflows.RAGResult) { r.Comparison.Snapshot = nil }},
		{"nil bucket", func(r *workflows.RAGResult) { r.Comparison.Dates[1].Result = nil }},
		{"bad diagnostics", func(r *workflows.RAGResult) { r.Comparison.Dates[1].Result.Rejected = map[string]string{"id": ""} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &comparisonService{mutate: tc.mutate}
			h, err := NewEvidence(service, EvidenceConfig{Token: token, Corpus: "corpus:test", Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, Concurrent: 1, Timeout: time.Second, EnableAnswers: true})
			if err != nil {
				t.Fatal(err)
			}
			r := new(pb.QuestionRequest)
			if err := protojson.Unmarshal([]byte(questionJSON), r); err != nil {
				t.Fatal(err)
			}
			r.TemporalScope.Mode = pb.TemporalMode_TEMPORAL_MODE_COMPARE
			r.TemporalScope.EffectiveAt = nil
			r.TemporalScope.CompareDates = []*pb.CalendarDate{{Year: 2026, Month: 1, Day: 1}, {Year: 2025, Month: 1, Day: 1}}
			raw, _ := protojson.Marshal(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, requestFixture(string(raw)))
			want := 502
			if tc.name == "success" {
				want = 200
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.name == "success" {
				var out struct {
					Mode  string
					Dates []struct {
						Date     json.RawMessage `json:"effective_date"`
						Evidence json.RawMessage
					}
				}
				if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Mode != "evidence_comparison" || len(out.Dates) != 2 {
					t.Fatal(w.Body.String())
				}
				for i, bucket := range out.Dates {
					d := new(pb.CalendarDate)
					if protojson.Unmarshal(bucket.Date, d) != nil || !proto.Equal(d, r.TemporalScope.CompareDates[i]) {
						t.Fatal("date order lost")
					}
					b := new(pb.EvidenceBundle)
					if protojson.Unmarshal(bucket.Evidence, b) != nil || b.Meta.RecordId != fmt.Sprintf("bundle:date-%d", i) {
						t.Fatal("lost C01 bundle")
					}
				}
				if w.Header().Get("X-Effective-Date") != "" || w.Header().Get("X-Snapshot-ID") != "snapshot:test" {
					t.Fatal(w.Header())
				}
			} else if strings.Contains(w.Body.String(), "bundle:date") {
				t.Fatal("partial output leaked")
			}
			req := requestFixture(string(raw))
			req.URL.Path = "/v1/questions"
			w = httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 400 || service.calls != 1 {
				t.Fatal("unsupported comparative answer reached execution", w.Code, service.calls)
			}
		})
	}
}

func TestComparisonAdmissionAndCancellation(t *testing.T) {
	for _, mode := range []string{"duplicate", "one", "too many", "effective date", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s := &comparisonService{}
			h, err := NewEvidence(s, EvidenceConfig{Token: token, Corpus: "corpus:test", Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, Concurrent: 1, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			r := new(pb.QuestionRequest)
			_ = protojson.Unmarshal([]byte(questionJSON), r)
			r.TemporalScope.Mode = pb.TemporalMode_TEMPORAL_MODE_COMPARE
			r.TemporalScope.EffectiveAt = nil
			r.TemporalScope.CompareDates = []*pb.CalendarDate{{Year: 2026, Month: 1, Day: 1}, {Year: 2025, Month: 1, Day: 1}}
			switch mode {
			case "duplicate":
				r.TemporalScope.CompareDates[1] = r.TemporalScope.CompareDates[0]
			case "one":
				r.TemporalScope.CompareDates = r.TemporalScope.CompareDates[:1]
			case "too many":
				for i := 0; i < 7; i++ {
					r.TemporalScope.CompareDates = append(r.TemporalScope.CompareDates, &pb.CalendarDate{Year: int32(2024 - i), Month: 1, Day: 1})
				}
			case "effective date":
				r.TemporalScope.EffectiveAt = &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}
			}
			raw, _ := protojson.Marshal(r)
			req := requestFixture(string(raw))
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			req = req.WithContext(ctx)
			if mode == "cancelled" {
				s.cancel = cancel
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			want, calls := 400, 0
			if mode == "cancelled" {
				want, calls = 504, 1
			}
			if w.Code != want || s.calls != calls {
				t.Fatal(w.Code, s.calls, w.Body.String())
			}
		})
	}
}

func TestComparisonJSONExpansionBudget(t *testing.T) {
	r := new(pb.QuestionRequest)
	if err := protojson.Unmarshal([]byte(questionJSON), r); err != nil {
		t.Fatal(err)
	}
	r.TemporalScope.Mode = pb.TemporalMode_TEMPORAL_MODE_COMPARE
	r.TemporalScope.EffectiveAt = nil
	for i := 0; i < 8; i++ {
		r.TemporalScope.CompareDates = append(r.TemporalScope.CompareDates, &pb.CalendarDate{Year: int32(2026 - i), Month: 1, Day: 1})
	}
	run, err := (&comparisonService{}).Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range run.Comparison.Dates {
		date.Result.Rejected = map[string]string{}
		for i := 0; i < 256; i++ {
			date.Result.Rejected[fmt.Sprintf("rejected:%d", i)] = strings.Repeat("<", 4096)
		}
	}
	if raw, err := schemas.MarshalEvidenceComparison(r, run, false); err == nil || raw != nil || !strings.Contains(err.Error(), "JSON byte budget") {
		t.Fatal("escaped aggregate diagnostics not bounded", len(raw), err)
	}
}
