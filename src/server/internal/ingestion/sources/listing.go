// Listing acquisition reads HTML only, preserving provenance for a later PDF queue.
// Integration: workflow checkpoints ListingObservation; this is a local D01 artifact, not C01 wire schema.
// Performance: one bounded HTML request per page, shared rate limits; never follows PDF links.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Extend bounded listing discovery with source-specific pagination and persistent page provenance.
// Bukti verifikasi: Test next-page cycles, duplicate URLs and incremental resume; distinguish URL count from unique regulations/PDFs.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package sources

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

type ListingObservation struct {
	URL        string    `json:"url"`
	FinalURL   string    `json:"final_url"`
	FetchedAt  time.Time `json:"fetched_at"`
	HTMLSHA256 string    `json:"html_sha256"`
	HTMLPath   string    `json:"html_path"`
	Page       Page      `json:"page"`
}

func (c *Collector) ReadListing(ctx context.Context, raw string) (ListingObservation, error) {
	obs := ListingObservation{URL: raw, FetchedAt: time.Now().UTC()}
	resp, err := c.get(ctx, raw)
	if err != nil {
		return obs, err
	}
	defer resp.Body.Close()
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return obs, errors.New("listing must be HTML; PDF body is not consumed")
	}
	body, err := readBounded(resp.Body, 8<<20)
	if err != nil {
		return obs, err
	}
	obs.FinalURL = resp.Request.URL.String()
	obs.HTMLSHA256 = digest(body)
	obs.HTMLPath = "listings/" + obs.HTMLSHA256 + ".html"
	obs.Page, err = ParsePage(body, obs.FinalURL)
	if err != nil {
		return obs, err
	}
	// Unsupported layouts stay retryable instead of becoming successful empty checkpoints.
	if len(obs.Page.DocumentURLs) == 0 && len(obs.Page.NextURLs) == 0 {
		return obs, errors.New("no supported document or pagination links found")
	}
	if err = writeAtomic(filepath.Join(c.opts.OutputDir, filepath.FromSlash(obs.HTMLPath)), body); err != nil {
		return obs, err
	}
	return obs, nil
}
