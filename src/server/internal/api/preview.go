// Serves the loopback interview UI and delegates questions to the preview workflow.
// Input size, deadline, same-origin requests and PDF lookup are bounded. Local PDF
// bytes must match an admitted D01 receipt; no caller-supplied filesystem paths are
// opened. Readiness describes loaded sample data, not model/GraphRAG release gates.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/workflows"
)

//go:embed preview.html
var previewHTML []byte

func NewPreviewHandler(flow *workflows.Preview, corpus *storage.PreviewCorpus, acquisition *os.Root, passages int, model, host string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(previewHTML)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"mode": "local_bm25_preview", "documents": len(corpus.PDFs), "pages": len(corpus.Pages), "passages": passages, "corpus_fingerprint": corpus.Fingerprint, "model": model})
	})
	mux.HandleFunc("POST /api/ask", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Question string `json:"question"`
			Generate bool   `json:"generate"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || len(input.Question) > 2000 || strings.TrimSpace(input.Question) == "" {
			http.Error(w, "Pertanyaan tidak valid (maksimal 2000 byte).", 400)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "Kirim satu objek JSON.", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
		defer cancel()
		result, err := flow.Ask(ctx, input.Question, input.Generate)
		if errors.Is(err, workflows.ErrPreviewBusy) {
			http.Error(w, "Model sedang menjawab. Coba kembali sebentar lagi.", 429)
			return
		}
		if err != nil {
			http.Error(w, "Pencarian gagal atau dibatalkan.", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /pdf/{hash}", func(w http.ResponseWriter, r *http.Request) {
		receipt, ok := corpus.PDFs[r.PathValue("hash")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		file, err := acquisition.Open(receipt.Path)
		if err != nil {
			http.Error(w, "PDF tidak tersedia.", 404)
			return
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, receipt.Bytes+1))
		sum := sha256.Sum256(raw)
		if err != nil || int64(len(raw)) != receipt.Bytes || hex.EncodeToString(sum[:]) != receipt.SHA256 {
			http.Error(w, "Integritas PDF tidak cocok.", 409)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="regulation.pdf"`)
		http.ServeContent(w, r, "regulation.pdf", time.Time{}, bytes.NewReader(raw))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if r.Host != host {
			http.Error(w, "Invalid local host", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "http" || u.Host != host {
				http.Error(w, "Cross-origin request refused", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
