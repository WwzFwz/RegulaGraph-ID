// CLI operasional: discover menyiapkan antrean, collect mengunduh sumber, dan audit memverifikasi inventory.
// Peran: merakit dependency serta argumen untuk workflow/adapter tanpa menggandakan parsing atau aturan domain.
// Kontrak: URL/file/listing dibatasi, output machine-readable, cancellation diteruskan, dan exit code membedakan
// invalid invocation (2), kegagalan/integrity error (1), serta keberhasilan terverifikasi (0).
// Benchmark: worker, per-host interval, timeout, batas bytes, dan concurrency audit dikonfigurasi; hasil akuisisi
// bukan bukti target retrieval/model dan angka required tidak berubah.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: discover/collect/audit D01 dan submit job durable dengan policy terpin aktif;
// job status/update/query ditambahkan saat workflow pemiliknya aktif.
// Bukti verifikasi: test exit code, output JSON, cancellation, dan budget deferral; ikuti doc/verification.md.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"regulagraph.local/server/internal/ingestion/sources"
	"regulagraph.local/server/internal/workflows"
)

type urlFlags []string

func (v *urlFlags) String() string     { return strings.Join(*v, ",") }
func (v *urlFlags) Set(s string) error { *v = append(*v, s); return nil }

func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || (args[0] != "collect" && args[0] != "discover" && args[0] != "audit" && args[0] != "submit") {
		fmt.Fprintln(errOut, "Usage: regulagraph {collect|discover|audit|submit}; use command -help for options.")
		return 2
	}
	if args[0] == "submit" {
		return runSubmit(ctx, args[1:], out, errOut)
	}
	if args[0] == "audit" {
		return runAudit(ctx, args[1:], out, errOut)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	var urls, listings urlFlags
	fs.Var(&urls, "url", "Detail page or direct PDF URL; repeatable")
	fs.Var(&listings, "listing", "Discover detail URLs from a listing page; repeatable (BPK/Komdigi layouts)")
	input := fs.String("input", "", "UTF-8 URL file: collect takes detail/PDF URLs, discover takes listing seeds; # comments allowed")
	output := fs.String("out", "data/acquisition", "Directory for PDFs, original HTML, records and observations")
	workers := fs.Int("workers", 2, "Concurrent documents, 1..16")
	interval := fs.Duration("interval", time.Second, "Minimum interval between requests to one host")
	timeout := fs.Duration("timeout", 60*time.Second, "HTTP request timeout; total per-document budget is three times this")
	maxMB := fs.Int64("max-pdf-mib", 100, "Maximum bytes per PDF in MiB, 1..4096")
	maxTotal := fs.Int64("max-total-pdf-bytes", 0, "Collect only: cap unique PDF bytes including existing blobs; 0 disables; single writer required")
	maxPages := fs.Int("max-pages", 5, "Listing page limit: collect per seed, discover total new pages/run; not a completeness claim")
	maxDocuments := fs.Int("max-documents", 20, "Collect only: maximum discovered documents per listing seed")
	refresh := fs.Bool("refresh", false, "Fetch again instead of reusing a complete checksum-verified record")
	related := fs.Bool("include-related", false, "Also download explicitly classified related judgments")
	if e := fs.Parse(args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *workers < 1 || *workers > 16 || *maxMB < 1 || *maxMB > 4096 || *maxPages < 1 || *maxDocuments < 1 || *interval < 0 || *timeout <= 0 || *maxTotal < 0 {
		fmt.Fprintln(errOut, "Invalid limits or unexpected positional arguments")
		return 2
	}
	if *input != "" {
		f, e := os.Open(*input)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
			if line != "" && !strings.HasPrefix(line, "#") {
				urls = append(urls, line)
			}
		}
		e = scanner.Err()
		f.Close()
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
	}
	collector, e := sources.NewCollector(sources.Options{OutputDir: *output, Interval: *interval, Timeout: *timeout, MaxPDFBytes: *maxMB << 20, Refresh: *refresh, IncludeRelated: *related, MaxTotalPDFBytes: *maxTotal})
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 2
	}
	discoveryFailed := false
	if args[0] == "discover" {
		// Input file is a listing seed list for discovery, a detail URL list for collect.
		seeds := append(append([]string(nil), listings...), urls...)
		if len(seeds) == 0 {
			fmt.Fprintln(errOut, "discover requires -listing or -input listing seeds")
			return 2
		}
		state, err := workflows.DiscoverSources(ctx, collector, seeds, *output, *maxPages, func(u string, n int, e error) {
			fmt.Fprintf(errOut, "Discovery queue=%d %s", n, u)
			if e != nil {
				fmt.Fprintf(errOut, " error=%v", e)
			}
			fmt.Fprintln(errOut)
		})
		writeErr := json.NewEncoder(out).Encode(map[string]any{"documents": len(state.URLs), "pages": len(state.Pages), "errors": state.Errors, "pdf_downloads": 0})
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if writeErr != nil {
			fmt.Fprintln(errOut, writeErr)
			return 1
		}
		return 0
	}
	for _, listing := range listings {
		discoveryCtx, cancel := context.WithTimeout(ctx, *timeout*3)
		found, e := collector.Discover(discoveryCtx, listing, *maxPages, *maxDocuments)
		cancel()
		urls = append(urls, found...)
		fmt.Fprintf(errOut, "Discovery: %d documents from %s (bounded by configured limits)\n", len(found), listing)
		if e != nil {
			fmt.Fprintln(errOut, e)
			discoveryFailed = true
		}
	}
	if len(urls) == 0 {
		fmt.Fprintln(errOut, "No documents: supply -input, -url, or a supported -listing")
		if discoveryFailed {
			return 1
		}
		return 2
	}
	encoder := json.NewEncoder(out)
	var outputErr error
	summary, e := workflows.CollectSources(ctx, collector, urls, *workers, func(event workflows.CollectionEvent) {
		if outputErr == nil {
			outputErr = encoder.Encode(event)
		}
		state := event.Result.Record.Status
		if event.Result.Reused {
			state = "reused"
		}
		if event.Error == sources.ErrPDFBudget.Error() {
			state = "deferred: " + event.Error
		} else if event.Error != "" {
			state = "failed: " + event.Error
		}
		fmt.Fprintf(errOut, "%s %s\n", state, event.URL)
	})
	if outputErr == nil {
		outputErr = encoder.Encode(map[string]any{"summary": summary, "unique_pdf_bytes": collector.PDFBytes(), "max_total_pdf_bytes": *maxTotal})
	}
	if e != nil {
		fmt.Fprintln(errOut, e)
	}
	if outputErr != nil {
		fmt.Fprintln(errOut, outputErr)
	}
	if e != nil || outputErr != nil || discoveryFailed {
		return 1
	}
	return 0
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
