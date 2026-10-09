// Extends the native INDEX test through the actual API composition root and HTTP
// handler, including graph-only when configured by the ASSEMBLE fixture. Cold
// concurrent and warm authenticated queries reuse dependencies while taking new
// snapshot leases; C01 evidence and provenance must survive JSON serialization.
// This remains synthetic source content, not a gold/latency acceptance dataset.
package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/api"
	"regulagraph.local/server/internal/api/routes"
)

type synchronizedAPILog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedAPILog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *synchronizedAPILog) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func verifyNativeEvidenceAPI(t *testing.T, ctx context.Context, dsn, root, native, qdrant, scope string, question *pb.QuestionRequest, snapshot *pb.SnapshotRef, configure ...func(*api.EvidenceRuntimeConfig)) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var before int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM snapshot_read_leases").Scan(&before); err != nil {
		t.Fatal(err)
	}
	cfg := api.EvidenceRuntimeConfig{DSN: dsn, ArtifactRoot: root, NativeEndpoint: native, QdrantEndpoint: qdrant, Corpus: question.CorpusId, AuthScope: scope, Build: "native-integration", Profile: question.RequestedProfile, Limit: 8, Timeout: 20 * time.Second}
	for _, option := range configure {
		option(&cfg)
	}
	runtime, err := api.OpenEvidenceRuntime(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	const token = "native-integration-only-operator-token"
	var logs synchronizedAPILog
	server, err := api.NewEvidenceServer("127.0.0.1:8097", runtime, routes.EvidenceConfig{Token: token, Corpus: question.CorpusId, Profile: question.RequestedProfile, Concurrent: 2, Timeout: cfg.Timeout, EnableAnswers: cfg.EnableAnswers}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// Race the first two queries through cold generation preparation. Both must
	// get their own evidence identity and lease while sharing immutable resources.
	httpServer := httptest.NewServer(server.Handler)
	defer httpServer.Close()
	client := &http.Client{Timeout: cfg.Timeout + 5*time.Second}
	raw, err := protojson.Marshal(question)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		id  string
		err error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			request, e := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/v1/evidence", bytes.NewReader(raw))
			if e != nil {
				results <- outcome{err: e}
				return
			}
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Content-Type", "application/json")
			response, e := client.Do(request)
			if e != nil {
				results <- outcome{err: e}
				return
			}
			encoded, e := io.ReadAll(io.LimitReader(response.Body, 16<<20))
			response.Body.Close()
			if e != nil {
				results <- outcome{err: e}
				return
			}
			if response.StatusCode != 200 {
				results <- outcome{err: fmt.Errorf("concurrent HTTP status %d: %s", response.StatusCode, encoded)}
				return
			}
			bundle := new(pb.EvidenceBundle)
			if e = protojson.Unmarshal(encoded, bundle); e != nil {
				results <- outcome{err: e}
				return
			}
			if len(bundle.Items) == 0 || !proto.Equal(bundle.Snapshot, snapshot) {
				results <- outcome{err: fmt.Errorf("concurrent output lost snapshot/evidence")}
				return
			}
			results <- outcome{id: bundle.Meta.RecordId}
		}()
	}
	close(start)
	seen := map[string]bool{}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.id == "" || seen[result.id] {
			t.Fatal("request reused evidence identity")
		}
		seen[result.id] = true
	}
	for _, path := range []string{"/livez", "/readyz", "/v1/evidence", "/v1/evidence"} {
		method := http.MethodGet
		var body io.Reader
		if path == "/v1/evidence" {
			method = http.MethodPost
			body = bytes.NewReader(raw)
		}
		request, e := http.NewRequestWithContext(ctx, method, httpServer.URL+path, body)
		if e != nil {
			t.Fatal(e)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, e := client.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		encoded, e := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		response.Body.Close()
		if e != nil || response.StatusCode != 200 {
			t.Fatalf("API %s: status %d %s %v", path, response.StatusCode, encoded, e)
		}
		if path == "/v1/evidence" {
			bundle := new(pb.EvidenceBundle)
			if e = protojson.Unmarshal(encoded, bundle); e != nil {
				t.Fatal(e)
			}
			if len(bundle.Items) == 0 || !proto.Equal(bundle.Snapshot, snapshot) || response.Header.Get("X-Evidence-ID") != bundle.Meta.RecordId {
				t.Fatal("HTTP evidence identity lost")
			}
			for _, item := range bundle.Items {
				if question.RequestedProfile == pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG && len(item.GraphPaths) == 0 {
					t.Fatal("graph API lost graph proof")
				}
				if len(item.SourceRefs) == 0 {
					t.Fatal("HTTP source provenance lost")
				}
			}
		}
	}
	if cfg.EnableAnswers {
		request, e := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/v1/questions", bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, e := client.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		encoded, e := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		response.Body.Close()
		if e != nil || response.StatusCode != 200 {
			t.Fatalf("native answer HTTP %d: %s %v", response.StatusCode, encoded, e)
		}
		var output struct {
			Mode     string
			Answer   json.RawMessage
			Evidence json.RawMessage
			Input    uint64 `json:"input_tokens"`
			Output   uint64 `json:"output_tokens"`
		}
		if e = json.Unmarshal(encoded, &output); e != nil {
			t.Fatal(e)
		}
		a, b := new(pb.Answer), new(pb.EvidenceBundle)
		if protojson.Unmarshal(output.Answer, a) != nil || protojson.Unmarshal(output.Evidence, b) != nil || output.Mode != "answer_draft" || !proto.Equal(a.Snapshot, snapshot) || !proto.Equal(b.Snapshot, snapshot) || output.Input == 0 || output.Output == 0 || response.Header.Get("X-Answer-ID") != a.Meta.RecordId {
			t.Fatal("native HTTP answer lost snapshot/accounting")
		}
		if a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL && a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN {
			t.Fatal("native draft promoted")
		}
		if a.SemanticStatus == pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL && len(a.Citations) == 0 {
			t.Fatal("native draft lost citations")
		}
		t.Logf("native HTTP answer PASS: input=%d output=%d status=%s citations=%d", output.Input, output.Output, a.SemanticStatus, len(a.Citations))
	}
	if bytes.Contains(logs.Bytes(), []byte(token)) || bytes.Contains(logs.Bytes(), []byte(question.Question)) || bytes.Contains(logs.Bytes(), []byte(dsn)) {
		t.Fatal("API logs disclosed request/credentials")
	}
	var leases int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM snapshot_read_leases").Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if leases != before {
		t.Fatal("HTTP request/readiness leaked read leases", leases)
	}
	t.Log("native HTTP: two concurrent cold queries, readiness and two warm queries PASS; no leaked leases")
}
