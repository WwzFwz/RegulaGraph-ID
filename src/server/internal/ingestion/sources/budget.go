// PDF volume budget counts unique verified blobs, including previous successful downloads.
// Integration: one collector process owns the output directory; promotion is serialized across workers.
// Performance: hashes existing blobs once at startup, streams new files, caps final PDF storage bytes.
// Temporary transfers and HTML are outside this corpus-volume cap; stop before a complete PDF would exceed it.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Keep total unique-byte accounting atomic at promotion; add cross-process ownership only when a multi-process collector is introduced.
// Bukti verifikasi: Exercise restart, duplicate hashes and concurrent near-cap writes; distinguish final PDF bytes from temporary/network traffic.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package sources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrPDFBudget = errors.New("PDF volume limit reached; next unique PDF does not fit")

func (c *Collector) initializeBudget() error {
	c.blobSizes = map[string]int64{}
	if c.opts.MaxTotalPDFBytes == 0 {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(c.opts.OutputDir, "blobs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pdf") {
			continue
		}
		hash := strings.TrimSuffix(entry.Name(), ".pdf")
		if !validHash(hash) || !verifyFile(filepath.Join(c.opts.OutputDir, "blobs", entry.Name()), hash) {
			return fmt.Errorf("cannot account for invalid existing PDF: %s", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		c.blobSizes[hash] = info.Size()
		c.blobBytes += info.Size()
	}
	if c.blobBytes > c.opts.MaxTotalPDFBytes {
		return errors.New("existing PDFs already exceed configured total cap; no files changed")
	}
	c.budgetStopped = c.blobBytes == c.opts.MaxTotalPDFBytes
	return nil
}
func (c *Collector) BudgetStopped() bool {
	c.blobMu.Lock()
	defer c.blobMu.Unlock()
	return c.budgetStopped
}
func (c *Collector) PDFBytes() int64 { c.blobMu.Lock(); defer c.blobMu.Unlock(); return c.blobBytes }
