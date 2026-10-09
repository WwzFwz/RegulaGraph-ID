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
// Status: evidence API empat profil dan optional local draft answers aktif.
// Graph route/policy dipin sebelum startup; graph-only tidak membutuhkan native.
// Integrasi berikutnya: streaming serta acceptance corpus/model/gold.
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
	"regulagraph.local/server/internal/retrieval/query"
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
	runtime.Normalization, err = query.ParseNormalizationMode(env("REGULAGRAPH_QUERY_NORMALIZATION"))
	if err != nil {
		return "", runtime, routes.EvidenceConfig{}, err
	}
	runtime.TimeZone = env("REGULAGRAPH_QUERY_TIME_ZONE")
	if _, err = query.LoadQueryTimeZone(runtime.TimeZone); err != nil {
		return "", runtime, routes.EvidenceConfig{}, err
	}
	runtime.GraphPath = env("REGULAGRAPH_QUERY_GRAPH_CONFIG")
	runtime.GraphHash = env("REGULAGRAPH_QUERY_GRAPH_CONFIG_SHA256")
	runtime.GraphUsername = env("REGULAGRAPH_NEO4J_USERNAME")
	runtime.GraphPassword = env("REGULAGRAPH_NEO4J_PASSWORD")
	if mode := env("REGULAGRAPH_API_ANSWERS"); mode != "" && mode != "false" {
		if mode != "true" {
			return "", runtime, routes.EvidenceConfig{}, errors.New("API answers must be true or false")
		}
		runtime.EnableAnswers = true
		runtime.AnswerPath = env("REGULAGRAPH_ANSWER_CONFIG")
		runtime.AnswerHash = env("REGULAGRAPH_ANSWER_CONFIG_SHA256")
		runtime.AnswerKey = env("REGULAGRAPH_ANSWER_API_KEY")
		if runtime.AnswerPath == "" || runtime.AnswerHash == "" {
			return "", runtime, routes.EvidenceConfig{}, errors.New("answer configuration pins required")
		}
	}
	if raw := env("REGULAGRAPH_API_TIMEOUT"); raw != "" {
		runtime.Timeout, err = time.ParseDuration(raw)
		if err != nil || runtime.Timeout < time.Second || runtime.Timeout > 5*time.Minute {
			return "", runtime, routes.EvidenceConfig{}, errors.New("API timeout must be between 1s and 5m")
		}
	}
	httpConfig := routes.EvidenceConfig{TimeZone: runtime.TimeZone, Token: env("REGULAGRAPH_API_TOKEN"), Corpus: runtime.Corpus, Profile: profile, Concurrent: 8, Timeout: runtime.Timeout}
	httpConfig.EnableAnswers = runtime.EnableAnswers
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
	startupLimit := 15 * time.Second
	if cfg.EnableAnswers {
		startupLimit = 2 * time.Minute
	}
	startup, cancel := context.WithTimeout(ctx, startupLimit)
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
	capability := "evidence"
	if cfg.EnableAnswers {
		capability = "evidence_and_answer_draft"
	}
	logger.Info("evidence_api_listening", "address", address, "capability", capability, "readiness", "check_readyz")
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
