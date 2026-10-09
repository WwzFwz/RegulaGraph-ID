// Entry point HTTP evidence lokal dengan lifecycle resource eksplisit.
//
// Peran dalam komponen:
// Menyusun runtime query terpin, autentikasi per corpus dan bounded HTTP listener.
//
// Integrasi dan perhatian performa:
// Startup membuka pool/client sekali; signal menghentikan admission dan drain
// request sebelum menutup dependency. Listener dibatasi loopback, bukan deployment.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: evidence API empat profil aktif; API jawaban/streaming belum aktif.
// Graph route/policy dipin sebelum startup; graph-only tidak membutuhkan native.
// Integrasi berikutnya: tokenizer/generator terpin.
// Bukti verifikasi: Test startup rollback, signals and graceful drain; no model load or connection at import.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/api"
	"regulagraph.local/server/internal/api/routes"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx))
}

func configuration(env func(string) string) (string, api.EvidenceRuntimeConfig, routes.EvidenceConfig, error) {
	address := env("REGULAGRAPH_API_LISTEN")
	if address == "" {
		address = "127.0.0.1:8097"
	}
	host, port, err := net.SplitHostPort(address)
	number, e := strconv.Atoi(port)
	if err != nil || e != nil || number < 1 || number > 65535 || !net.ParseIP(host).IsLoopback() {
		return "", api.EvidenceRuntimeConfig{}, routes.EvidenceConfig{}, errors.New("loopback API address required")
	}
	profile := map[string]pb.RetrievalProfile{"vector": pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG, "hybrid": pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, "graph": pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, "hybrid-graph": pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG}[env("REGULAGRAPH_API_PROFILE")]
	runtime := api.EvidenceRuntimeConfig{DSN: env("REGULAGRAPH_POSTGRES_DSN"), ArtifactRoot: env("REGULAGRAPH_ARTIFACTS_DIR"), NativeEndpoint: env("REGULAGRAPH_QUERY_NATIVE_ENDPOINT"), QdrantEndpoint: env("REGULAGRAPH_QDRANT_URL"), QdrantKey: env("REGULAGRAPH_QDRANT_API_KEY"), Corpus: env("REGULAGRAPH_QUERY_CORPUS_ID"), AuthScope: env("REGULAGRAPH_API_AUTH_SCOPE"), Build: env("REGULAGRAPH_BUILD_ID"), Profile: profile, Limit: 20, Timeout: 30 * time.Second}
	runtime.GraphPath = env("REGULAGRAPH_QUERY_GRAPH_CONFIG")
	runtime.GraphHash = env("REGULAGRAPH_QUERY_GRAPH_CONFIG_SHA256")
	runtime.GraphUsername = env("REGULAGRAPH_NEO4J_USERNAME")
	runtime.GraphPassword = env("REGULAGRAPH_NEO4J_PASSWORD")
	httpConfig := routes.EvidenceConfig{Token: env("REGULAGRAPH_API_TOKEN"), Corpus: runtime.Corpus, Profile: profile, Concurrent: 8, Timeout: runtime.Timeout}
	if err := routes.ValidateEvidenceConfig(httpConfig); err != nil {
		return "", runtime, httpConfig, err
	}
	return address, runtime, httpConfig, nil
}

func run(ctx context.Context) int {
	address, cfg, httpConfig, err := configuration(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid evidence API configuration; see doc/evidence-api.md")
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	runtime, err := api.OpenEvidenceRuntime(startup, cfg)
	cancel()
	if err != nil {
		logger.Error("evidence_api_startup_failed")
		return 1
	}
	defer runtime.Close()
	server, err := api.NewEvidenceServer(address, runtime, httpConfig, logger)
	if err != nil {
		logger.Error("evidence_api_http_config_failed")
		return 2
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		logger.Error("evidence_api_listen_failed")
		return 1
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	logger.Info("evidence_api_listening", "address", address, "capability", "evidence", "readiness", "check_readyz")
	select {
	case err = <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("evidence_api_serve_failed")
			return 1
		}
		return 0
	case <-ctx.Done():
		drain, cancel := context.WithTimeout(context.Background(), cfg.Timeout+5*time.Second)
		defer cancel()
		if err = server.Shutdown(drain); err != nil {
			_ = server.Close()
			logger.Error("evidence_api_drain_timeout")
			return 1
		}
		<-finished
		logger.Info("evidence_api_stopped")
		return 0
	}
}
