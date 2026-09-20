// Behavioral acquisition tests with synthetic HTML/PDF envelopes and an in-memory HTTP transport.
// Role: verify provenance, real byte storage, resume, partial failure, limits, and link classification.
// Integration: no live network or legal facts; format envelopes do not prove full PDF parser validity.
// Performance: bounded fixtures; production throughput/latency requires separate measured workloads.
package sources

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const examplePDF = "%PDF-1.4\n% synthetic download envelope, not a legal document\ntrailer\n<<>>\n%%EOF\n"

func fakeResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}
}
func testCollector(t *testing.T, fn roundTripFunc) *Collector {
	t.Helper()
	c, e := NewCollector(Options{OutputDir: t.TempDir(), Timeout: time.Second, MaxPDFBytes: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	c.client.Transport = fn
	return c
}

func TestParsePortalMetadataAndPrimaryVersusRelated(t *testing.T) {
	raw := `<h1>UU &amp; Contoh</h1><div><div>Nomor</div><div>27</div></div><div><div>Tanggal Berlaku</div><div>unknown</div></div><a href="/Read/1/main.pdf">Preview</a><a href="/Download/1/main.pdf">Download</a><a href="/DownloadUjiMateri/2/court.pdf">Putusan</a><script><a href="/fake.pdf">bad</a></script>`
	p, e := ParsePage([]byte(raw), "https://peraturan.bpk.go.id/Details/1/example")
	if e != nil {
		t.Fatal(e)
	}
	if p.Title != "UU & Contoh" || p.Fields["number"][0] != "27" || p.Fields["effective_date"][0] != "unknown" {
		t.Fatalf("metadata: %+v", p)
	}
	if len(p.PDFs) != 1 || !strings.Contains(p.PDFs[0].URL, "/Download/1/") || len(p.RelatedPDFs) != 1 {
		t.Fatalf("PDF classification: %+v", p)
	}
	p, e = ParsePage([]byte(`<h1>Contoh</h1><div>Nomor<br><span>20</span></div><div>Tahun<br><span>2016</span></div><a href="/produk_hukum/unduh/id/553/t/example">Unduh</a>`), "https://jdih.komdigi.go.id/produk_hukum/view/id/553/t/example")
	if e != nil || p.Fields["number"][0] != "20" || p.Fields["year"][0] != "2016" || len(p.PDFs) != 1 {
		t.Fatalf("Komdigi: %+v %v", p, e)
	}
}

func TestCollectSavesPDFAndResumesWithoutNetwork(t *testing.T) {
	calls := 0
	c := testCollector(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(req.URL.Path, "Details") {
			return fakeResponse(req, 200, `<h1>Contoh</h1><a href="/Download/1/example.pdf">PDF</a>`), nil
		}
		return fakeResponse(req, 200, examplePDF), nil
	})
	url := "https://peraturan.bpk.go.id/Details/1/example"
	r, e := c.Collect(context.Background(), url)
	if e != nil {
		t.Fatal(e)
	}
	if r.Record.Status != "complete" || len(r.Record.PDFs) != 1 {
		t.Fatalf("%+v", r)
	}
	pdf := r.Record.PDFs[0]
	b, e := os.ReadFile(filepath.Join(c.opts.OutputDir, filepath.FromSlash(pdf.Path)))
	if e != nil || string(b) != examplePDF || pdf.SHA256 != digest(b) {
		t.Fatalf("PDF was not saved correctly: %v", e)
	}
	if r.Record.HTMLSHA256 == "" {
		t.Fatal("missing raw HTML provenance")
	}
	r, e = c.Collect(context.Background(), url)
	if e != nil || !r.Reused || calls != 2 {
		t.Fatalf("resume: calls=%d reused=%v err=%v", calls, r.Reused, e)
	}
	c.opts.Refresh = true
	r, e = c.Collect(context.Background(), url)
	if e != nil || r.Reused || calls != 4 {
		t.Fatalf("refresh: %d %v", calls, e)
	}
}

func TestInvalidOversizeAndTruncatedPDFNotPublished(t *testing.T) {
	for name, body := range map[string]string{"html": "<html>Access denied</html>", "truncated": "%PDF-1.4\nmissing trailer", "oversize": examplePDF + strings.Repeat(" ", 1024)} {
		t.Run(name, func(t *testing.T) {
			c := testCollector(t, func(req *http.Request) (*http.Response, error) { return fakeResponse(req, 200, body), nil })
			c.opts.MaxPDFBytes = 256
			r := c.download(context.Background(), PDFLink{URL: "https://peraturan.bpk.go.id/Download/1/file.pdf", Kind: "document"})
			if r.Error == "" || r.Path != "" || r.SHA256 != "" {
				t.Fatalf("invalid response published: %+v", r)
			}
			files, _ := filepath.Glob(filepath.Join(c.opts.OutputDir, "blobs", "*"))
			if len(files) != 0 {
				t.Fatalf("temporary file leaked: %v", files)
			}
		})
	}
}

func TestPartialFailureRetriesOnlyUnfinishedPDF(t *testing.T) {
	goodCalls := 0
	badCalls := 0
	recoverBad := false
	c := testCollector(t, func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "Details") {
			return fakeResponse(req, 200, `<a href="/Download/1/good.pdf">PDF</a><a href="/Download/2/bad.pdf">Annex</a>`), nil
		}
		if strings.Contains(req.URL.Path, "good") {
			goodCalls++
			return fakeResponse(req, 200, examplePDF), nil
		}
		badCalls++
		if recoverBad {
			return fakeResponse(req, 200, examplePDF), nil
		}
		return fakeResponse(req, 200, "<html>error</html>"), nil
	})
	raw := "https://peraturan.bpk.go.id/Details/1/example"
	r, e := c.Collect(context.Background(), raw)
	if e == nil || r.Record.Status != "partial" {
		t.Fatalf("expected partial: %+v %v", r, e)
	}
	recoverBad = true
	r, e = c.Collect(context.Background(), raw)
	if e != nil || r.Record.Status != "complete" || goodCalls != 1 || badCalls != 2 {
		t.Fatalf("partial resume: %+v %v calls %d/%d", r, e, goodCalls, badCalls)
	}
}

func TestRetryAfterAndURLPolicy(t *testing.T) {
	attempts := 0
	c := testCollector(t, func(req *http.Request) (*http.Response, error) {
		attempts++
		r := fakeResponse(req, 503, "")
		r.Header.Set("Retry-After", "0")
		if attempts == 2 {
			r = fakeResponse(req, 200, examplePDF)
		}
		return r, nil
	})
	r := c.download(context.Background(), PDFLink{URL: "https://peraturan.bpk.go.id/Download/1/file.pdf"})
	if r.Error != "" || attempts != 2 {
		t.Fatalf("retry failed: %+v attempts=%d", r, attempts)
	}
	for _, u := range []string{"http://peraturan.bpk.go.id/x", "https://localhost/a.pdf", "https://peraturan.bpk.go.id.evil.test/a.pdf", "https://user:password@peraturan.bpk.go.id/x", "https://peraturan.bpk.go.id:444/x"} {
		if validateURL(u) == nil {
			t.Fatalf("unsafe URL accepted: %s", u)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := pause(ctx, time.Hour); e == nil {
		t.Fatal("cancelled wait succeeded")
	}
}

func TestDiscoveryIsBoundedAndDoesNotExecuteJavaScript(t *testing.T) {
	calls := 0
	c := testCollector(t, func(req *http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(req, 200, `<a href="/Details/1/one">One</a><a href="/Details/1/one">Duplicate</a><a href="/Details/2/two">Two</a><a rel="next" href="?page=2">Next</a>`), nil
	})
	found, e := c.Discover(context.Background(), "https://peraturan.bpk.go.id/", 1, 1)
	if e != nil || len(found) != 1 || calls != 1 {
		t.Fatalf("bounds: %v %v calls %d", found, e, calls)
	}
	p, e := ParsePage([]byte(`<iframe src="/viewer?file=%2Ffiles%2Fa.pdf"></iframe><script>location='/secret.pdf'</script>`), "https://jdih.komdigi.go.id/")
	if e != nil || len(p.PDFs) != 1 || !strings.HasSuffix(p.PDFs[0].URL, "/files/a.pdf") {
		t.Fatalf("embedded PDF: %+v %v", p, e)
	}
}

func TestUnknownSizeStreamIsBounded(t *testing.T) {
	c := testCollector(t, func(req *http.Request) (*http.Response, error) {
		r := fakeResponse(req, 200, examplePDF+strings.Repeat("x", 512))
		r.ContentLength = -1
		return r, nil
	})
	c.opts.MaxPDFBytes = 128
	r := c.download(context.Background(), PDFLink{URL: "https://peraturan.bpk.go.id/a.pdf"})
	if r.Error == "" {
		t.Fatal("chunked oversized response accepted")
	}
	if _, e := readBounded(bytes.NewBufferString("12345"), 4); e == nil {
		t.Fatal("HTML size limit ignored")
	}
}

func TestListingTableHeadersDoNotPolluteMetadata(t *testing.T) {
	raw := `<div>Nomor<br><span>20</span></div><div>Tahun<br><span>2016</span></div><table><tr><th>Judul</th><th>Nomor</th><th>Tahun</th><th>Status</th></tr></table>`
	p, err := ParsePage([]byte(raw), "https://jdih.komdigi.go.id/produk_hukum/view/id/553/t/example")
	if err != nil || len(p.Fields["number"]) != 1 || len(p.Fields["year"]) != 1 || len(p.Fields["title"]) != 0 {
		t.Fatalf("table headers polluted metadata: %+v %v", p, err)
	}
}

func TestCorruptLocalPDFIsDownloadedAndRepaired(t *testing.T) {
	c := testCollector(t, func(req *http.Request) (*http.Response, error) { return fakeResponse(req, 200, examplePDF), nil })
	u := "https://peraturan.bpk.go.id/Download/1/file.pdf"
	r, err := c.Collect(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(c.opts.OutputDir, filepath.FromSlash(r.Record.PDFs[0].Path))
	if err = os.WriteFile(file, []byte("corrupted"), 0644); err != nil {
		t.Fatal(err)
	}
	r, err = c.Collect(context.Background(), u)
	if err != nil || r.Reused || !verifyFile(file, digest([]byte(examplePDF))) {
		t.Fatalf("repair failed: %+v %v", r, err)
	}
}

func TestRedirectToUnconfiguredHostIsRejected(t *testing.T) {
	c := testCollector(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() != "peraturan.bpk.go.id" {
			t.Fatal("request escaped host policy")
		}
		r := fakeResponse(req, 302, "")
		r.Header.Set("Location", "https://example.com/private.pdf")
		return r, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.get(ctx, "https://peraturan.bpk.go.id/a.pdf")
	if err == nil {
		t.Fatal("forbidden redirect accepted")
	}
}

func TestBPKAdjacentPaginationAndJDIHNDiscovery(t *testing.T) {
	p, e := ParsePage([]byte(`<a href="/Search?tahun=2025&amp;p=2">2</a><a href="/Search?tahun=2025&amp;p=11">Next</a>`), "https://peraturan.bpk.go.id/Search?tahun=2025")
	if e != nil || len(p.NextURLs) != 1 || !strings.HasSuffix(p.NextURLs[0], "p=2") {
		t.Fatalf("pagination %+v %v", p, e)
	}
	p, e = ParsePage([]byte(`<a href="/doc/123">Example regulation</a>`), "https://jdihn.go.id/")
	if e != nil || len(p.DocumentURLs) != 1 || p.DocumentTitles[p.DocumentURLs[0]] != "Example regulation" {
		t.Fatalf("JDIHN %+v %v", p, e)
	}
}
func TestReadListingNeverConsumesPDF(t *testing.T) {
	c := testCollector(t, func(req *http.Request) (*http.Response, error) {
		r := fakeResponse(req, 200, examplePDF)
		r.Header.Set("Content-Type", "application/pdf")
		return r, nil
	})
	if _, e := c.ReadListing(context.Background(), "https://peraturan.bpk.go.id/Download/1/example.pdf"); e == nil {
		t.Fatal("PDF accepted as listing")
	}
	entries, e := os.ReadDir(c.opts.OutputDir)
	if e != nil || len(entries) != 0 {
		t.Fatalf("unexpected files: %v %v", entries, e)
	}
}
