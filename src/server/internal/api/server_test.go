// Verifies HTTP lifecycle on a real loopback listener: liveness is independent
// of model readiness, shutdown stops new connections and drains an accepted
// request, and configured time/header limits exist before serving. Fake workflow
// output is deliberate; native pipeline tests cover the real runtime separately.
package api

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/api/routes"
)

type lifecycleEvidence struct{ entered, release chan struct{} }

func (s *lifecycleEvidence) Search(ctx context.Context, _ *pb.QuestionRequest) (*pb.EvidenceBundle, error) {
	close(s.entered)
	select {
	case <-s.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *lifecycleEvidence) Ready(context.Context) error { return context.DeadlineExceeded }

type closingListener struct {
	net.Listener
	closed chan struct{}
}

func (l *closingListener) Close() error {
	err := l.Listener.Close()
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return err
}

func TestEvidenceServerGracefulDrain(t *testing.T) {
	service := &lifecycleEvidence{make(chan struct{}), make(chan struct{})}
	cfg := routes.EvidenceConfig{Token: "operator-test-token-with-32-characters", Corpus: "corpus:test", Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, Concurrent: 1, Timeout: 5 * time.Second}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if _, err := NewEvidenceServer("0.0.0.0:8097", service, cfg, logger); err == nil {
		t.Fatal("nonlocal listener accepted")
	}
	server, err := NewEvidenceServer("127.0.0.1:0", service, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatal("missing transport bounds")
	}
	rawListener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	listener := &closingListener{rawListener, make(chan struct{})}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	url := "http://" + listener.Addr().String()
	response, err := client.Get(url + "/livez")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("liveness depends on readiness")
	}
	if !strings.HasPrefix(response.Header.Get("X-Request-ID"), "api:") {
		t.Fatal("missing server request identity")
	}
	request, err := http.NewRequest("POST", url+"/v1/evidence", strings.NewReader(`{"corpus_id":"corpus:test","question":"izin?","response_mode":"RESPONSE_MODE_COMPLETE","requested_profile":"RETRIEVAL_PROFILE_HYBRID_RAG","temporal_scope":{"mode":"TEMPORAL_MODE_AS_OF","effective_at":{"year":2026,"month":1,"day":1},"unresolved_policy":"UNRESOLVED_POLICY_REPORT"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+cfg.Token)
	completed := make(chan error, 1)
	go func() {
		r, e := client.Do(request)
		if e == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode != 502 {
				e = context.Canceled
			}
		}
		completed <- e
	}()
	select {
	case <-service.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not enter workflow")
	}
	drained := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		drained <- server.Shutdown(ctx)
	}()
	<-listener.closed
	select {
	case err := <-drained:
		t.Fatal("shutdown failed to wait for accepted request", err)
	default:
	}
	close(service.release)
	if err := <-completed; err != nil {
		t.Fatal("accepted request cancelled during graceful drain", err)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
