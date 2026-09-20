// Mengekspos audit corpus lokal sebagai perintah CLI dengan JSON summary dan exit code stabil.
// Peran: meneruskan root/concurrency ke adapter sources lalu melaporkan manifest tanpa menghitung ulang audit.
// Kontrak: exit 0 hanya bila audit selesai dan integrity_valid=true; input salah exit 2, I/O/cancel/integrity exit 1.
// Benchmark: command tidak memuat PDF ke RAM; throughput hashing dan peak RSS diukur pada corpus referensi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: perintah `regulagraph audit` aktif untuk inventory D01 lokal.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"

	"regulagraph.local/server/internal/ingestion/sources"
)

func runAudit(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(errOut)
	root := fs.String("out", "data/acquisition", "Acquisition directory containing records, observations, pages, blobs, and queue.txt")
	defaultWorkers := runtime.GOMAXPROCS(0)
	if defaultWorkers > 16 {
		defaultWorkers = 16
	}
	workers := fs.Int("workers", defaultWorkers, "Concurrent PDF hash workers, 1..32")
	maxSamples := fs.Int("max-issue-samples", 100, "Maximum issue examples retained in inventory.json, 1..10000")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *root == "" || *workers < 1 || *workers > 32 || *maxSamples < 1 || *maxSamples > 10000 {
		fmt.Fprintln(errOut, "Invalid audit root, workers, issue sample limit, or unexpected positional arguments")
		return 2
	}
	audit, err := sources.AuditAcquisition(ctx, sources.AuditOptions{Root: *root, Workers: *workers, MaxIssueSamples: *maxSamples})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(audit.Manifest); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if !audit.Manifest.IntegrityValid {
		fmt.Fprintf(errOut, "inventory contains %d integrity errors; inspect %s/inventory.json\n", audit.Manifest.IntegrityErrors, *root)
		return 1
	}
	return 0
}
