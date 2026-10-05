// Verifies preview manifests fail closed on corrupt bytes, path escapes and
// unowned pages. The fixture uses original D01 schema, not a new worker contract.
package storage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regulagraph.local/server/internal/ingestion/sources"
	"strings"
	"testing"
)

func TestPreviewManifestIntegrity(t *testing.T) {
	root := t.TempDir()
	blob := strings.Repeat("a", 64)
	_ = os.Mkdir(filepath.Join(root, blob), 0700)
	record := sources.Record{SchemaVersion: 1, Status: "complete", SourceURL: "https://example.org/regulation", Metadata: sources.Page{Title: "Sumber"}, PDFs: []sources.PDFReceipt{{SHA256: blob, Kind: "document", Bytes: 10, Path: "blobs/a.pdf"}}}
	raw, _ := json.Marshal(record)
	text := []byte("Pasal 1. Bukti teks asli sumber untuk pengujian.")
	manifest := ""
	for name, data := range map[string][]byte{blob + "/record.json": raw, blob + "/page-00001.txt": text} {
		if e := os.WriteFile(filepath.Join(root, name), data, 0600); e != nil {
			t.Fatal(e)
		}
		manifest += fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)
	}
	write := func(s string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(manifest)
	c, e := LoadPreviewCorpus(root)
	if e != nil || len(c.Pages) != 1 || c.Pages[0].Text != string(text) {
		t.Fatalf("load: %v", e)
	}
	write(manifest + strings.Split(manifest, "\n")[0] + "\n")
	if _, e = LoadPreviewCorpus(root); e == nil {
		t.Fatal("duplicate accepted")
	}
	write(fmt.Sprintf("%x  ../outside.txt\n", sha256.Sum256(text)))
	if _, e = LoadPreviewCorpus(root); e == nil {
		t.Fatal("path escape accepted")
	}
	write(manifest)
	_ = os.WriteFile(filepath.Join(root, blob, "page-00001.txt"), []byte("changed"), 0600)
	if _, e = LoadPreviewCorpus(root); e == nil {
		t.Fatal("corruption accepted")
	}
}
