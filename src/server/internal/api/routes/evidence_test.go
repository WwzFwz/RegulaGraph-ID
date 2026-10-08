// Exercises HTTP authorization, C01 decoding, overload and cancellation before
// expensive retrieval. Fake services isolate the transport boundary; actual
// storage/model integration lives in indexing's native pipeline test. Error
// bodies must not disclose backend secrets or turn failures into success.
package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const questionJSON = `{"corpus_id":"corpus:test","question":"Apa ketentuan izin?","response_mode":"RESPONSE_MODE_COMPLETE","requested_profile":"RETRIEVAL_PROFILE_HYBRID_RAG","temporal_scope":{"mode":"TEMPORAL_MODE_AS_OF","effective_at":{"year":2026,"month":1,"day":1},"unresolved_policy":"UNRESOLVED_POLICY_REPORT"}}`
const token = "test-token-with-at-least-32-characters"

type evidenceFake struct {
	calls  atomic.Int32
	search func(context.Context) (*pb.EvidenceBundle, error)
	ready  error
}

func (f *evidenceFake) Search(ctx context.Context, _ *pb.QuestionRequest) (*pb.EvidenceBundle, error) {
	f.calls.Add(1)
	return f.search(ctx)
}
func (f *evidenceFake) Ready(context.Context) error { return f.ready }
func bundleFixture() *pb.EvidenceBundle {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	return &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:test", RecordId: "bundle:test"}, Snapshot: &pb.SnapshotRef{CorpusId: "corpus:test", SnapshotId: "snapshot:test", Sequence: 1, ManifestHash: hash, RepresentationGeneration: "generation:test"}, RetrievalManifest: &pb.ProducerManifest{SchemaVersion: 1, Software: "test", Build: "test", ConfigHash: hash}, Completeness: pb.Completeness_COMPLETENESS_NONE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
}
func handlerFixture(t *testing.T, f *evidenceFake) *Evidence {
	t.Helper()
	h, e := NewEvidence(f, EvidenceConfig{Token: token, Corpus: "corpus:test", Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, Concurrent: 1, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func requestFixture(body string) *http.Request {
	r := httptest.NewRequest("POST", "http://127.0.0.1/v1/evidence", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestEvidenceHTTPAdmission(t *testing.T) {
	f := &evidenceFake{search: func(context.Context) (*pb.EvidenceBundle, error) { return bundleFixture(), nil }}
	h := handlerFixture(t, f)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		body   string
		status int
	}{
		{"valid", nil, questionJSON, 200},
		{"missing token", func(r *http.Request) { r.Header.Del("Authorization") }, questionJSON, 401},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, questionJSON, 401},
		{"duplicate token", func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+token) }, questionJSON, 401},
		{"browser", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, questionJSON, 403},
		{"method", func(r *http.Request) { r.Method = "GET" }, questionJSON, 405},
		{"content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, questionJSON, 415},
		{"query params", func(r *http.Request) { r.URL.RawQuery = "key=secret" }, questionJSON, 400},
		{"foreign corpus", nil, strings.Replace(questionJSON, "corpus:test", "corpus:foreign", 1), 403},
		{"wrong profile", nil, strings.Replace(questionJSON, "HYBRID_RAG", "GRAPH_RAG", 1), 400},
		{"trusted context injection", nil, strings.TrimSuffix(questionJSON, "}") + `,"context":{"auth_scope_ref":"admin"}}`, 400},
		{"duplicate field", nil, strings.TrimSuffix(questionJSON, "}") + `,"question":"override"}`, 400},
		{"trailing", nil, questionJSON + ` {}`, 400},
		{"oversized", nil, strings.Repeat(" ", 65<<10), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requestFixture(tc.body)
			if tc.mutate != nil {
				tc.mutate(r)
			}
			w := httptest.NewRecorder()
			before := f.calls.Load()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.status != 200 && f.calls.Load() != before {
				t.Fatal("invalid input dispatched")
			}
			if tc.status == 200 {
				var decoded pb.EvidenceBundle
				if e := protojson.Unmarshal(w.Body.Bytes(), &decoded); e != nil {
					t.Fatal(e)
				}
				if decoded.Completeness != pb.Completeness_COMPLETENESS_NONE {
					t.Fatal("empty evidence became complete")
				}
			}
		})
	}
}

func TestEvidenceFailuresAndReadiness(t *testing.T) {
	f := &evidenceFake{search: func(context.Context) (*pb.EvidenceBundle, error) {
		return nil, errors.New("postgres://secret-password")
	}, ready: errors.New("native failure secret")}
	h := handlerFixture(t, f)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, requestFixture(questionJSON))
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("error disclosure or success", w.Code, w.Body.String())
	}
	r := requestFixture("")
	r.Method = "GET"
	r.URL.Path = "/readyz"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("false readiness")
	}
	f.ready = nil
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("readiness failed")
	}
	f.search = func(context.Context) (*pb.EvidenceBundle, error) { return nil, nil }
	w = httptest.NewRecorder()
	h.ServeHTTP(w, requestFixture(questionJSON))
	if w.Code != 502 {
		t.Fatal("nil output accepted")
	}
	f.search = func(context.Context) (*pb.EvidenceBundle, error) {
		b := bundleFixture()
		b.Meta.CorpusId = "corpus:foreign"
		return b, nil
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, requestFixture(questionJSON))
	if w.Code != 502 {
		t.Fatal("foreign output accepted")
	}
}

func TestEvidenceOverloadAndCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	f := &evidenceFake{search: func(ctx context.Context) (*pb.EvidenceBundle, error) {
		close(entered)
		select {
		case <-release:
			return bundleFixture(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	h := handlerFixture(t, f)
	finished := make(chan struct{})
	go func() { defer close(finished); h.ServeHTTP(httptest.NewRecorder(), requestFixture(questionJSON)) }()
	<-entered
	w := httptest.NewRecorder()
	h.ServeHTTP(w, requestFixture(questionJSON))
	if w.Code != 429 || f.calls.Load() != 1 {
		t.Fatal("overload not bounded")
	}
	close(release)
	<-finished
	f.search = func(ctx context.Context) (*pb.EvidenceBundle, error) { <-ctx.Done(); return nil, ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, requestFixture(questionJSON).WithContext(ctx))
	if w.Code != 504 || len(h.slots) != 0 {
		t.Fatal("deadline/admission leak", w.Code)
	}
}
