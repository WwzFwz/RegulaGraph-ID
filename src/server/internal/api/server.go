// Menjadi tempat konstruksi aplikasi Go HTTP server dan lifecycle resource.
//
// Peran dalam komponen:
// Menggabungkan routes, dependency wiring, dan pemetaan error HTTP.
//
// Kontrak integrasi dan perhatian implementasi:
// Local evidence HTTP memiliki body/header/deadline/concurrency limits dan bearer
// authorization per corpus. Resource dibuka eksplisit; import tidak memulai layanan.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: evidence server aktif; livez hanya process liveness, readyz memeriksa
// snapshot/model/backend. Optional complete draft answers active; streaming pending.
// Integrasi berikutnya: streaming dan distributed observability.
// Bukti verifikasi: Exercise slow clients, cancelled streams, overload and shutdown with in-flight requests; measure queue-inclusive latency.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"regulagraph.local/server/internal/api/routes"
)

func NewEvidenceServer(address string, service routes.EvidenceService, cfg routes.EvidenceConfig, logger *slog.Logger) (*http.Server, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() || logger == nil {
		return nil, errors.New("loopback listen address and logger required")
	}
	evidence, err := routes.NewEvidence(service, cfg)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/evidence", evidence)
	mux.Handle("/v1/questions", evidence)
	mux.Handle("/readyz", evidence)
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "{\"status\":\"alive\"}\n")
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		id := "api:" + hex.EncodeToString(nonce[:])
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), evidenceRequestIdentity{}, id))
		meter := &evidenceResponseWriter{ResponseWriter: w, status: 200}
		mux.ServeHTTP(meter, r)
		// Never log query text, tokens, credentials or arbitrary URL parameters.
		route := "unknown"
		if r.URL.Path == "/v1/evidence" || r.URL.Path == "/v1/questions" || r.URL.Path == "/readyz" || r.URL.Path == "/livez" {
			route = r.URL.Path
		}
		logger.Info("http_request", "route", route, "status", meter.status, "duration_ms", float64(time.Since(start).Microseconds())/1000, "request_id", id, "evidence_id", w.Header().Get("X-Evidence-ID"), "answer_id", w.Header().Get("X-Answer-ID"))
	})
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.Timeout, WriteTimeout: cfg.Timeout + 5*time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

type evidenceResponseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *evidenceResponseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *evidenceResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
