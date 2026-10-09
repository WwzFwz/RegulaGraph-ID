// Verifies complete draft HTTP admission, shared overload/cancellation and output
// identity/status gates. Fixtures exercise transport failures; actual generation
// and snapshot lease lifetime are covered by indexing's opt-in native graph test.
// Passing these tests is not evidence of semantic or benchmark acceptance.
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/workflows"
)

type answerFake struct {
	*evidenceFake
	answer func(context.Context, *pb.QuestionRequest) (*workflows.RAGResult, error)
}

func (f *answerFake) AnswerEnabled() bool { return f.answer != nil }

func (f *answerFake) Answer(ctx context.Context, q *pb.QuestionRequest) (*workflows.RAGResult, error) {
	return f.answer(ctx, q)
}
func answerFixture(q *pb.QuestionRequest) *workflows.RAGResult {
	b := bundleFixture()
	a := &pb.Answer{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: q.CorpusId, RecordId: "answer:test"}, RequestId: "request:test", Text: answering.AbstainText, Snapshot: proto.Clone(b.Snapshot).(*pb.SnapshotRef), EffectiveDates: []*pb.CalendarDate{q.TemporalScope.EffectiveAt}, RunManifest: b.RetrievalManifest, SemanticStatus: pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	return &workflows.RAGResult{Evidence: b, Answer: &workflows.EvidenceAnswerResult{Draft: &answering.DraftResult{Answer: a, InputTokens: 42, OutputTokens: 12}}}
}
func answerHandler(t *testing.T, f *answerFake) *Evidence {
	t.Helper()
	h, e := NewEvidence(f, EvidenceConfig{Token: token, Corpus: "corpus:test", Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, Concurrent: 1, Timeout: time.Second, EnableAnswers: true})
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func answerRequest() *http.Request {
	r := requestFixture(questionJSON)
	r.URL.Path = "/v1/questions"
	return r
}

func TestAnswerOutputAdmission(t *testing.T) {
	for _, mode := range []string{"success", "error", "nil", "missing draft", "mixed output", "foreign corpus", "foreign snapshot", "promoted", "date", "deadline", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := &answerFake{evidenceFake: &evidenceFake{search: func(context.Context) (*pb.EvidenceBundle, error) {
				t.Fatal("answer fell back to search")
				return nil, nil
			}}}
			f.answer = func(ctx context.Context, q *pb.QuestionRequest) (*workflows.RAGResult, error) {
				r := answerFixture(q)
				switch mode {
				case "error":
					return nil, errors.New("secret model path and key")
				case "nil":
					return nil, nil
				case "mixed output":
					r.Comparison = &workflows.RAGComparisonResult{}
				case "missing draft":
					r.Answer = nil
				case "foreign corpus":
					r.Answer.Draft.Answer.Meta.CorpusId = "corpus:other"
				case "foreign snapshot":
					r.Answer.Draft.Answer.Snapshot.SnapshotId = "snapshot:other"
				case "promoted":
					r.Answer.Draft.Answer.SemanticStatus = pb.SemanticStatus_SEMANTIC_STATUS_COMPLETE
				case "date":
					r.Answer.Draft.Answer.EffectiveDates = []*pb.CalendarDate{{Year: 2025, Month: 1, Day: 1}}
				case "deadline":
					return nil, context.DeadlineExceeded
				case "cancelled":
					return nil, context.Canceled
				}
				return r, nil
			}
			w := httptest.NewRecorder()
			answerHandler(t, f).ServeHTTP(w, answerRequest())
			want := 502
			switch mode {
			case "success":
				want = 200
			case "error":
				want = 503
			case "deadline":
				want = 504
			case "cancelled":
				want = 408
			}
			if w.Code != want || strings.Contains(w.Body.String(), "secret") {
				t.Fatal(w.Code, w.Body.String())
			}
			if mode == "success" {
				var response struct {
					Mode     string
					Answer   json.RawMessage
					Evidence json.RawMessage
					Input    uint64 `json:"input_tokens"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				a := new(pb.Answer)
				b := new(pb.EvidenceBundle)
				if protojson.Unmarshal(response.Answer, a) != nil || protojson.Unmarshal(response.Evidence, b) != nil || response.Mode != "answer_draft" || response.Input != 42 || !proto.Equal(a.Snapshot, b.Snapshot) || w.Header().Get("X-Answer-ID") != a.Meta.RecordId {
					t.Fatal("lost C01 identity/accounting")
				}
			}
		})
	}
}

func TestAnswerSharesAdmissionAndCancellation(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan struct{})
	f := &answerFake{evidenceFake: &evidenceFake{search: func(context.Context) (*pb.EvidenceBundle, error) { t.Fatal("overload bypassed"); return nil, nil }}, answer: func(ctx context.Context, _ *pb.QuestionRequest) (*workflows.RAGResult, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	h := answerHandler(t, f)
	// Invalid callers must not enter the generator.
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Header.Set("Origin", "https://example.org") }, func(r *http.Request) { r.Method = "GET" }} {
		r := answerRequest()
		mutate(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatal("invalid request admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	go func() { defer close(finished); h.ServeHTTP(w, answerRequest().WithContext(ctx)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("answer not dispatched")
	}
	for _, path := range []string{"/v1/evidence", "/v1/questions", "/readyz"} {
		r := requestFixture(questionJSON)
		r.URL.Path = path
		if path == "/readyz" {
			r.Method = "GET"
		}
		other := httptest.NewRecorder()
		h.ServeHTTP(other, r)
		if other.Code != 429 {
			t.Fatal("separate admission pools", path, other.Code)
		}
	}
	cancel()
	<-finished
	if w.Code != 408 {
		t.Fatal("cancellation lost", w.Code)
	}
}

func TestAnswerCapabilityIsExplicit(t *testing.T) {
	f := &evidenceFake{search: func(context.Context) (*pb.EvidenceBundle, error) { return bundleFixture(), nil }}
	h := handlerFixture(t, f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, answerRequest())
	if w.Code != 404 {
		t.Fatal("answer enabled implicitly")
	}
	cfg := h.config
	cfg.Token = token
	cfg.EnableAnswers = true
	if _, err := NewEvidence(f, cfg); err == nil {
		t.Fatal("missing generator accepted")
	}
}
