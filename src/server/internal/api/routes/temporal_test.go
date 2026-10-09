// Exercises CURRENT HTTP admission, frozen-date headers and answer validation.
// Synthetic services expose transport trust boundaries without claiming corpus
// quality or benchmark acceptance. Session tests cover the one-clock guarantee.
package routes

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

type temporalService struct {
	calls   int
	corrupt string
}

func (s *temporalService) Ready(context.Context) error { return nil }
func (s *temporalService) AnswerEnabled() bool         { return true }
func (s *temporalService) Search(ctx context.Context, request *pb.QuestionRequest) (*workflows.RAGResult, error) {
	return s.Answer(ctx, request)
}
func (s *temporalService) Answer(_ context.Context, request *pb.QuestionRequest) (*workflows.RAGResult, error) {
	s.calls++
	zone, _ := query.LoadQueryTimeZone("Asia/Jakarta")
	scope, audit, err := query.ResolveTemporalScope(request.TemporalScope, zone, func() time.Time { return time.Date(2025, 12, 31, 17, 0, 0, 0, time.UTC) })
	if err != nil {
		return nil, err
	}
	owned := proto.Clone(request).(*pb.QuestionRequest)
	owned.TemporalScope = scope
	result := answerFixture(owned)
	result.Temporal = audit
	switch s.corrupt {
	case "missing":
		result.Temporal = nil
	case "date":
		result.Answer.Draft.Answer.EffectiveDates[0].Year++
	case "audit":
		result.Temporal.EffectiveDate.Year++
	case "zone":
		result.Temporal.TimeZone = "Asia/Bangkok" // Same date/offset, different operator policy.
	}
	return result, nil
}

func TestCurrentHTTPDateAudit(t *testing.T) {
	for _, path := range []string{"/v1/evidence", "/v1/questions"} {
		for _, mode := range []string{"success", "disabled", "supplied date", "missing", "audit", "zone", "date"} {
			if path == "/v1/evidence" && mode == "date" {
				continue
			}
			t.Run(path+mode, func(t *testing.T) {
				service := &temporalService{corrupt: mode}
				cfg := EvidenceConfig{Token: token, Corpus: "corpus:test", Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, Concurrent: 1, Timeout: time.Second, EnableAnswers: true, TimeZone: "Asia/Jakarta"}
				if mode == "disabled" {
					cfg.TimeZone = ""
				}
				h, err := NewEvidence(service, cfg)
				if err != nil {
					t.Fatal(err)
				}
				q := new(pb.QuestionRequest)
				if err := protojson.Unmarshal([]byte(questionJSON), q); err != nil {
					t.Fatal(err)
				}
				q.TemporalScope.Mode = pb.TemporalMode_TEMPORAL_MODE_CURRENT
				if mode != "supplied date" {
					q.TemporalScope.EffectiveAt = nil
				}
				raw, _ := protojson.Marshal(q)
				req := requestFixture(string(raw))
				req.URL.Path = path
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				want := 502
				if mode == "success" {
					want = 200
				}
				if mode == "disabled" || mode == "supplied date" {
					want = 400
					if service.calls != 0 {
						t.Fatal("invalid scope reached workflow")
					}
				}
				if w.Code != want {
					t.Fatal(w.Code, w.Body.String())
				}
				if mode == "success" && (w.Header().Get("X-Effective-Date") != "2026-01-01" || w.Header().Get("X-Query-Time-Zone") != "Asia/Jakarta" || strings.Contains(w.Body.String(), "2025")) {
					t.Fatal("wrong resolved calendar", w.Header(), w.Body.String())
				}
			})
		}
	}
}
