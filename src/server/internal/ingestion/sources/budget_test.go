// Budget regression tests use synthetic PDF envelopes to exercise real concurrent file promotion.
// Existing blobs, duplicate bytes and overflow are tested; this is not a PDF quality benchmark.
package sources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPDFBudgetDoesNotHideDownloadFailure(t *testing.T) {
	c := testCollector(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/Details/") {
			return fakeResponse(r, 200, `<h1>Fixture</h1><a href="/Download/1/missing.pdf">Missing</a><a href="/Download/2/large.pdf">Large</a>`), nil
		}
		if strings.Contains(r.URL.Path, "missing.pdf") {
			return fakeResponse(r, 404, "missing"), nil
		}
		return fakeResponse(r, 200, examplePDF), nil
	})
	c.opts.MaxTotalPDFBytes = int64(len(examplePDF) - 1)
	result, err := c.Collect(context.Background(), "https://peraturan.bpk.go.id/Details/1/fixture")
	if err == nil || errors.Is(err, ErrPDFBudget) {
		t.Fatalf("non-budget failure hidden: %v", err)
	}
	if !c.BudgetStopped() || len(result.Record.PDFs) != 2 {
		t.Fatalf("expected mixed failure and budget stop: %+v", result)
	}
	if result.Record.PDFs[0].Error == "" || result.Record.PDFs[1].Error != ErrPDFBudget.Error() {
		t.Fatalf("missing per-PDF errors: %+v", result.Record.PDFs)
	}
}

func TestPDFBudgetExistingDuplicatesAndOverflow(t *testing.T) {
	c := testCollector(t, func(r *http.Request) (*http.Response, error) { return fakeResponse(r, 200, examplePDF), nil })
	a, e := c.Collect(context.Background(), "https://peraturan.bpk.go.id/Download/1/a.pdf")
	if e != nil {
		t.Fatal(e)
	}
	n := int64(len(examplePDF))
	opts := c.opts
	opts.MaxTotalPDFBytes = n + 1
	next, e := NewCollector(opts)
	if e != nil {
		t.Fatal(e)
	}
	next.client.Transport = c.client.Transport
	b, e := next.Collect(context.Background(), "https://peraturan.bpk.go.id/Download/2/b.pdf")
	if e != nil || b.Record.PDFs[0].SHA256 != a.Record.PDFs[0].SHA256 || next.PDFBytes() != n {
		t.Fatalf("duplicate %v", e)
	}
	next.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return fakeResponse(r, 200, examplePDF+"\n"), nil })
	_, e = next.Collect(context.Background(), "https://peraturan.bpk.go.id/Download/3/c.pdf")
	if e != ErrPDFBudget || !next.BudgetStopped() || next.PDFBytes() != n {
		t.Fatalf("overflow %v", e)
	}
	entries, _ := os.ReadDir(filepath.Join(opts.OutputDir, "blobs"))
	if len(entries) != 1 {
		t.Fatal("overflow file persisted")
	}
	opts.MaxTotalPDFBytes = n - 1
	if _, e = NewCollector(opts); e == nil {
		t.Fatal("existing overflow accepted")
	}
}
func TestPDFBudgetConcurrentPromotion(t *testing.T) {
	n := int64(len(examplePDF) + 1)
	c, e := NewCollector(Options{OutputDir: t.TempDir(), Timeout: time.Second, MaxPDFBytes: 1 << 20, MaxTotalPDFBytes: n * 2})
	if e != nil {
		t.Fatal(e)
	}
	c.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return fakeResponse(r, 200, examplePDF+r.URL.Query().Get("id")), nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Collect(context.Background(), fmt.Sprintf("https://peraturan.bpk.go.id/Download/test.pdf?id=%d", i))
		}(i)
	}
	wg.Wait()
	if c.PDFBytes() > n*2 {
		t.Fatal("concurrent overshoot")
	}
	entries, _ := os.ReadDir(filepath.Join(c.opts.OutputDir, "blobs"))
	if len(entries) != 2 {
		t.Fatalf("expected two complete blobs, got %d", len(entries))
	}
}
