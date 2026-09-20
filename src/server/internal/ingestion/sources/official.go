// Akuisisi detail portal dan PDF nyata beserta metadata/provenance untuk inventory D01.
// Peran: menghubungkan fetch, parser metadata, blob store, dan receipt yang dapat dilanjutkan.
// Integrasi: belum melakukan OCR/chunking/graph/publication; metadata portal belum fakta hukum tervalidasi.
// Performa: bounded download, checksum reuse, record partial/error; benchmark SOURCE belum diklaim lulus.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Extend metadata/PDF extraction using captured layouts and preserve observation history; treat dates/status as unverified source assertions.
// Bukti verifikasi: Test changed layouts, attachment vs regulation links and missing metadata; report per-source extraction coverage.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package sources

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"time"
)

func (c *Collector) Collect(ctx context.Context, rawURL string) (Result, error) {
	if c.BudgetStopped() {
		return Result{}, ErrPDFBudget
	}
	if err := validateURL(rawURL); err != nil {
		return Result{}, err
	}
	old, exists := c.readRecord(rawURL)
	if exists && !c.opts.Refresh && old.Status == "complete" && old.ParserVersion == MetadataParserVersion {
		valid := len(old.PDFs) > 0
		for _, p := range old.PDFs {
			if !c.validReceipt(p) {
				valid = false
			}
		}
		if old.HTMLSHA256 != "" {
			valid = valid && old.HTMLPath == filepath.ToSlash(filepath.Join("pages", old.HTMLSHA256+".html")) && verifyFile(filepath.Join(c.opts.OutputDir, filepath.FromSlash(old.HTMLPath)), old.HTMLSHA256)
		}
		if c.opts.IncludeRelated {
			for _, link := range old.Metadata.RelatedPDFs {
				found := false
				for _, p := range old.PDFs {
					if p.URL == link.URL {
						found = true
					}
				}
				valid = valid && found
			}
		}
		if valid {
			return Result{Record: old, Reused: true}, nil
		}
	}
	u, _ := url.Parse(rawURL)
	r := Record{SchemaVersion: 1, ParserVersion: MetadataParserVersion, SourceURL: rawURL, Portal: u.Hostname(), FetchedAt: time.Now().UTC(), Status: "failed"}
	// A per-document deadline bounds retries, rate waiting, detail and attachments together.
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout*3)
	defer cancel()
	err := func() error {
		resp, e := c.get(ctx, rawURL)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		r.FinalURL = resp.Request.URL.String()
		reader := bufio.NewReader(resp.Body)
		prefix, _ := reader.Peek(1024)
		if bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("%PDF-")) {
			r.PDFs = []PDFReceipt{c.savePDF(resp, reader, PDFLink{URL: rawURL, Kind: "document"}, r.FetchedAt)}
		} else {
			body, e := readBounded(reader, 8<<20)
			if e != nil {
				return e
			}
			r.HTMLSHA256 = digest(body)
			r.HTMLPath = filepath.ToSlash(filepath.Join("pages", r.HTMLSHA256+".html"))
			if e = writeAtomic(filepath.Join(c.opts.OutputDir, filepath.FromSlash(r.HTMLPath)), body); e != nil {
				return e
			}
			r.Metadata, e = ParsePage(body, r.FinalURL)
			if e != nil {
				return e
			}
			links := append([]PDFLink{}, r.Metadata.PDFs...)
			if c.opts.IncludeRelated {
				links = append(links, r.Metadata.RelatedPDFs...)
			}
			if len(links) == 0 {
				return fmt.Errorf("no downloadable PDF found on detail page: %s", rawURL)
			}
			if len(links) > 50 {
				return fmt.Errorf("detail page contains %d PDFs; refusing ambiguous bulk page", len(links))
			}
			cached := map[string]PDFReceipt{}
			if !c.opts.Refresh {
				for _, p := range old.PDFs {
					if c.validReceipt(p) {
						cached[p.URL] = p
					}
				}
			}
			for _, link := range links {
				if c.BudgetStopped() {
					r.PDFs = append(r.PDFs, PDFReceipt{URL: link.URL, Kind: link.Kind, Error: ErrPDFBudget.Error()})
					continue
				}
				if p, ok := cached[link.URL]; ok {
					r.PDFs = append(r.PDFs, p)
				} else {
					r.PDFs = append(r.PDFs, c.download(ctx, link))
				}
			}
		}
		successful := 0
		for _, p := range r.PDFs {
			if p.Error == "" {
				successful++
			}
		}
		if successful != len(r.PDFs) {
			if successful > 0 {
				r.Status = "partial"
			}
			for _, p := range r.PDFs {
				if p.Error != "" && p.Error != ErrPDFBudget.Error() {
					return fmt.Errorf("downloaded %d/%d PDFs; non-budget failure: %s", successful, len(r.PDFs), p.Error)
				}
			}
			return ErrPDFBudget
		}
		r.Status = "complete"
		return nil
	}()
	if err != nil {
		r.Error = err.Error()
	}
	if saveErr := c.saveRecord(r); saveErr != nil {
		return Result{Record: r}, fmt.Errorf("save acquisition record: %w", saveErr)
	}
	return Result{Record: r}, err
}
