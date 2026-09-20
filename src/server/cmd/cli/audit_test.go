// Menguji boundary CLI audit: validasi argumen, JSON summary, file output, dan exit integrity.
// Peran: memastikan CLI tidak melaporkan corpus korup sebagai sukses atau menyembunyikan manifest audit.
// Kontrak: fixture memakai record collector nyata dan PDF envelope kecil; tidak membuktikan kualitas isi hukum.
// Benchmark: correctness saja; throughput dan peak RSS corpus 3 GB diukur pada run D01 terpisah.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: regression test perintah audit aktif.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"regulagraph.local/server/internal/ingestion/sources"
)

const auditPDF = "%PDF-1.4\n% audit fixture\ntrailer\n<<>>\n%%EOF\n"

func TestAuditCommandWritesMachineReadableInventory(t *testing.T) {
	root := t.TempDir()
	url := "https://peraturan.bpk.go.id/Details/1/example"
	pdfHash := testSHA256([]byte(auditPDF))
	html := []byte("<html><body>source</body></html>")
	htmlHash := testSHA256(html)
	record := sources.Record{SchemaVersion: 1, ParserVersion: sources.MetadataParserVersion, SourceURL: url, FinalURL: url,
		Portal: "peraturan.bpk.go.id", FetchedAt: time.Unix(1, 0).UTC(), Status: "complete", Metadata: sources.Page{Title: "Example"},
		HTMLSHA256: htmlHash, HTMLPath: "pages/" + htmlHash + ".html",
		PDFs: []sources.PDFReceipt{{URL: "https://peraturan.bpk.go.id/Download/1/example.pdf", Kind: "document", SHA256: pdfHash, Path: "blobs/" + pdfHash + ".pdf", Bytes: int64(len(auditPDF))}}}
	writeCLIJSON(t, filepath.Join(root, "records", testSHA256([]byte(url))+".json"), record)
	writeCLIJSON(t, filepath.Join(root, "observations", "1-"+testSHA256([]byte(url))+".json"), record)
	writeCLIFile(t, filepath.Join(root, "blobs", pdfHash+".pdf"), []byte(auditPDF))
	writeCLIFile(t, filepath.Join(root, "pages", htmlHash+".html"), html)
	writeCLIFile(t, filepath.Join(root, "queue.txt"), []byte(url+"\n"))

	var out, errs bytes.Buffer
	code := run(context.Background(), []string{"audit", "-out", root, "-workers", "2"}, &out, &errs)
	if code != 0 {
		t.Fatalf("audit exit=%d stderr=%s", code, errs.String())
	}
	var manifest sources.InventoryManifest
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.IntegrityValid || manifest.Records != 1 || manifest.UniquePDFs != 1 || manifest.QueueAcquired != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	for _, file := range []string{"inventory.json", "inventory.records.jsonl"} {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Fatalf("missing %s: %v", file, err)
		}
	}
}

func TestAuditCommandRejectsInvalidArgumentsAndBrokenInventory(t *testing.T) {
	for _, args := range [][]string{{"audit", "-workers", "0"}, {"audit", "unexpected"}, {"audit", "-max-issue-samples", "0"}} {
		var out, errs bytes.Buffer
		if code := run(context.Background(), args, &out, &errs); code != 2 {
			t.Fatalf("args=%v exit=%d stderr=%s", args, code, errs.String())
		}
	}
	root := t.TempDir()
	for _, dir := range []string{"records", "observations", "blobs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCLIFile(t, filepath.Join(root, "records", "broken.json"), []byte("not-json"))
	var out, errs bytes.Buffer
	if code := run(context.Background(), []string{"audit", "-out", root}, &out, &errs); code != 1 {
		t.Fatalf("broken inventory exit=%d stderr=%s", code, errs.String())
	}
}

func testSHA256(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func writeCLIJSON(t *testing.T, file string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, file, raw)
}

func writeCLIFile(t *testing.T, file string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
