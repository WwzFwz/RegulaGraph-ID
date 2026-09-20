// HTTP acquisition dengan allowlist portal, redirect checks, timeout, retry dan rate per host.
// Peran: berbagi koneksi dan kebijakan jaringan untuk discovery, detail, serta PDF streaming.
// Integrasi: hanya collector lokal D01; endpoint produksi, autentikasi dan publication belum aktif.
// Performa: antrean context-aware; respons/body terbatas; kegagalan tidak disamarkan sebagai sukses.
package sources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Options struct {
	OutputDir      string
	Interval       time.Duration
	Timeout        time.Duration
	MaxPDFBytes    int64
	Refresh        bool
	IncludeRelated bool
}
type Collector struct {
	opts   Options
	client *http.Client
	mu     sync.Mutex
	next   map[string]time.Time
}

func NewCollector(opts Options) (*Collector, error) {
	if opts.OutputDir == "" || opts.Interval < 0 || opts.Timeout <= 0 || opts.MaxPDFBytes <= 0 {
		return nil, errors.New("invalid collector output/interval/timeout/max size")
	}
	c := &Collector{opts: opts, next: map[string]time.Time{}}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 4
	c.client = &http.Client{Transport: transport, Timeout: opts.Timeout}
	c.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if err := validateURL(req.URL.String()); err != nil {
			return err
		}
		return c.wait(req.Context(), req.URL.Hostname())
	}
	return c, nil
}
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.User != nil || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return errors.New("only HTTPS portal URLs without credentials/nonstandard ports are allowed")
	}
	switch strings.ToLower(u.Hostname()) {
	case "peraturan.bpk.go.id", "jdih.komdigi.go.id", "jdihn.go.id", "www.jdihn.go.id":
		return nil
	default:
		return fmt.Errorf("host is not a configured source portal: %s", u.Hostname())
	}
}
func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (c *Collector) wait(ctx context.Context, host string) error {
	c.mu.Lock()
	now := time.Now()
	slot := c.next[host]
	if slot.Before(now) {
		slot = now
	}
	c.next[host] = slot.Add(c.opts.Interval)
	c.mu.Unlock()
	return pause(ctx, time.Until(slot))
}
func (c *Collector) get(ctx context.Context, raw string) (*http.Response, error) {
	if err := validateURL(raw); err != nil {
		return nil, err
	}
	u, _ := url.Parse(raw)
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if err := c.wait(ctx, u.Hostname()); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "RegulaGraph-ID/0.1 (+https://github.com/WwzFwz/RegulaGraph-ID; corpus acquisition)")
		req.Header.Set("Accept", "application/pdf,text/html;q=0.9,*/*;q=0.5")
		resp, err := c.client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		delay := time.Duration(1<<attempt) * time.Second
		retry := err != nil
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("HTTP %d for %s", resp.StatusCode, raw)
			retry = resp.StatusCode == 429 || resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
			if value := resp.Header.Get("Retry-After"); value != "" {
				if seconds, e := strconv.Atoi(value); e == nil && seconds >= 0 {
					delay = time.Duration(seconds) * time.Second
				} else if until, e := http.ParseTime(value); e == nil {
					delay = time.Until(until)
				}
			}
			resp.Body.Close()
		}
		if !retry || attempt == 2 {
			break
		}
		if err := pause(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, last
}
func readBounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("response exceeds %d bytes", max)
	}
	return b, nil
}

// Discover reads bounded listing pages. It reports unsupported layouts and never claims complete coverage.
func (c *Collector) Discover(ctx context.Context, start string, maxPages, maxDocuments int) ([]string, error) {
	if maxPages < 1 || maxDocuments < 1 {
		return nil, errors.New("discovery limits must be positive")
	}
	queue := []string{start}
	seenPages := map[string]bool{}
	seenDocs := map[string]bool{}
	var docs []string
	for len(queue) > 0 && len(seenPages) < maxPages && len(docs) < maxDocuments {
		raw := queue[0]
		queue = queue[1:]
		if seenPages[raw] {
			continue
		}
		seenPages[raw] = true
		resp, err := c.get(ctx, raw)
		if err != nil {
			return docs, err
		}
		body, err := readBounded(resp.Body, 8<<20)
		resp.Body.Close()
		if err != nil {
			return docs, err
		}
		page, err := ParsePage(body, resp.Request.URL.String())
		if err != nil {
			return docs, err
		}
		for _, d := range page.DocumentURLs {
			if !seenDocs[d] && len(docs) < maxDocuments {
				if e := validateURL(d); e != nil {
					continue
				}
				seenDocs[d] = true
				docs = append(docs, d)
			}
		}
		queue = append(queue, page.NextURLs...)
	}
	if len(docs) == 0 {
		return nil, errors.New("no supported document links found; provide detail/PDF URLs or inspect portal layout")
	}
	return docs, nil
}
