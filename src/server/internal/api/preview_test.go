// Exercises local UI security and request bounds without live model dependencies.
// Serving model output as text is complemented by host/origin and path admission.
package api

import (
	"net/http/httptest"
	"regulagraph.local/server/internal/adapters/storage"
	"strings"
	"testing"
)

func TestPreviewHTTPBoundary(t *testing.T) {
	h := NewPreviewHandler(nil, &storage.PreviewCorpus{}, nil, 0, "", "127.0.0.1:8096")
	for _, test := range []struct {
		method, path, host, origin, body string
		code                             int
	}{
		{"GET", "/", "127.0.0.1:8096", "", "", 200},
		{"GET", "/api/status", "127.0.0.1:8096", "", "", 200},
		{"GET", "/", "evil.example", "", "", 403},
		{"POST", "/api/ask", "127.0.0.1:8096", "http://evil.example", `{"question":"q"}`, 403},
		{"POST", "/api/ask", "127.0.0.1:8096", "", `{"question":""}`, 400},
		{"POST", "/api/ask", "127.0.0.1:8096", "", `{"question":"q"} {}`, 400},
		{"GET", "/pdf/unknown", "127.0.0.1:8096", "", "", 404},
	} {
		r := httptest.NewRequest(test.method, "http://"+test.host+test.path, strings.NewReader(test.body))
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatalf("%s %s: %d want %d", test.method, test.path, w.Code, test.code)
		}
	}
}
