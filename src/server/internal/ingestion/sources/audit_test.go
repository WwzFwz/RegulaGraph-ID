// Menguji audit inventory D01 dengan record, observation, queue, HTML, dan PDF nyata berukuran kecil.
// Peran: membuktikan manifest deterministik, deduplikasi blob, coverage, serta penolakan provenance korup.
// Kontrak: fixture memakai schema collector aktif; hasil audit tidak dianggap canonical identity atau gold data.
// Benchmark: test ini hanya correctness dan cancellation; throughput corpus 3 GB diukur sebagai run terpisah.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: regression test audit inventory aktif.
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditAcquisitionBuildsDeterministicInventory(t *testing.T) {
	root := t.TempDir()
	pdfHash := digest([]byte(examplePDF))
	writeAuditFile(t, root, filepath.Join("blobs", pdfHash+".pdf"), []byte(examplePDF))

	firstURL := "https://peraturan.bpk.go.id/Details/1/example"
	secondURL := "https://jdih.komdigi.go.id/produk_hukum/view/id/2/t/example"
	writeAuditRecord(t, root, firstURL, Record{
		SchemaVersion: 1, ParserVersion: MetadataParserVersion, SourceURL: firstURL, FinalURL: firstURL,
		Portal: "peraturan.bpk.go.id", FetchedAt: time.Unix(10, 0).UTC(), Status: "complete",
		Metadata: Page{Title: "Peraturan Contoh", Fields: map[string][]string{"regulation_type": {"Peraturan"}, "number": {"1"}, "year": {"2020"}, "issuer": {"Instansi"}}, DocumentURLs: []string{secondURL}},
		PDFs:     []PDFReceipt{{URL: "https://peraturan.bpk.go.id/Download/1/example.pdf", Kind: "document", SHA256: pdfHash, Path: filepath.ToSlash(filepath.Join("blobs", pdfHash+".pdf")), Bytes: int64(len(examplePDF)), ContentType: "application/pdf"}},
	})
	writeAuditRecord(t, root, secondURL, Record{
		SchemaVersion: 1, ParserVersion: MetadataParserVersion, SourceURL: secondURL, FinalURL: secondURL,
		Portal: "jdih.komdigi.go.id", FetchedAt: time.Unix(20, 0).UTC(), Status: "complete",
		Metadata: Page{Title: "Mirror Peraturan Contoh", Fields: map[string][]string{"regulation_type": {"Peraturan"}, "number": {"1"}, "year": {"2020"}, "issuer": {"Instansi"}}, DocumentURLs: []string{"https://peraturan.bpk.go.id/Details/404/missing"}},
		PDFs:     []PDFReceipt{{URL: "https://jdih.komdigi.go.id/unduh/2", Kind: "document", SHA256: pdfHash, Path: filepath.ToSlash(filepath.Join("blobs", pdfHash+".pdf")), Bytes: int64(len(examplePDF)), ContentType: "application/pdf"}},
	})
	writeAuditFile(t, root, "queue.txt", []byte(firstURL+"\n"+secondURL+"\nhttps://peraturan.bpk.go.id/Details/3/pending\n"))

	first, err := AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	manifest := first.Manifest
	if !manifest.IntegrityValid || manifest.Records != 2 || manifest.Observations != 2 || manifest.UniquePDFs != 1 || manifest.ReferencedPDFs != 1 {
		t.Fatalf("unexpected manifest counts: %+v", manifest)
	}
	if manifest.UniquePDFBytes != int64(len(examplePDF)) || manifest.QueueURLs != 3 || manifest.QueueAcquired != 2 || manifest.QueuePending != 1 {
		t.Fatalf("unexpected byte/queue summary: %+v", manifest)
	}
	if manifest.DocumentReferences != 2 || manifest.MissingDocumentRefs != 1 || manifest.CandidateIdentityGroups != 1 || manifest.CandidateCollisions != 1 {
		t.Fatalf("unexpected reference/identity summary: %+v", manifest)
	}
	if manifest.FormatClassification != "unknown_until_m01" || manifest.RecordsSHA256 == "" || manifest.BlobSetSHA256 == "" || manifest.InventoryID == "" {
		t.Fatalf("missing deterministic identity: %+v", manifest)
	}
	rows, err := os.ReadFile(filepath.Join(root, "inventory.records.jsonl"))
	if err != nil || digest(rows) != manifest.RecordsSHA256 {
		t.Fatalf("record output mismatch: %v", err)
	}
	second, err := AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.Manifest.InventoryID != manifest.InventoryID || second.Manifest.RecordsSHA256 != manifest.RecordsSHA256 || second.Manifest.BlobSetSHA256 != manifest.BlobSetSHA256 {
		t.Fatalf("same inputs produced different inventory identity: %s != %s", second.Manifest.InventoryID, manifest.InventoryID)
	}
	newHTML := []byte("<html><body>changed source provenance</body></html>")
	newHTMLHash := digest(newHTML)
	writeAuditFile(t, root, filepath.Join("pages", newHTMLHash+".html"), newHTML)
	key := digest([]byte(firstURL))
	var changed Record
	latestPath := filepath.Join(root, "records", key+".json")
	latestRaw, err := os.ReadFile(latestPath)
	if err != nil || json.Unmarshal(latestRaw, &changed) != nil {
		t.Fatalf("read latest fixture: %v", err)
	}
	changed.HTMLSHA256 = newHTMLHash
	changed.HTMLPath = filepath.ToSlash(filepath.Join("pages", newHTMLHash+".html"))
	changedRaw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	writeAuditFile(t, root, filepath.Join("records", key+".json"), changedRaw)
	writeAuditFile(t, root, filepath.Join("observations", "2-"+key+".json"), changedRaw)
	third, err := AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !third.Manifest.IntegrityValid || third.Manifest.InventoryID == manifest.InventoryID || third.Manifest.ObservationsSHA256 == manifest.ObservationsSHA256 {
		t.Fatalf("provenance change did not alter inventory identity: before=%s after=%s", manifest.InventoryID, third.Manifest.InventoryID)
	}
}

func TestAuditAcquisitionValidatesHistoricalReceiptsAndRequiredPDFs(t *testing.T) {
	root := t.TempDir()
	pdfHash := digest([]byte(examplePDF))
	writeAuditFile(t, root, filepath.Join("blobs", pdfHash+".pdf"), []byte(examplePDF))
	url := "https://peraturan.bpk.go.id/Details/7/example"
	missingURL := "https://peraturan.bpk.go.id/Download/7/missing.pdf"
	record := Record{SchemaVersion: 1, ParserVersion: MetadataParserVersion, SourceURL: url, FinalURL: url,
		Portal: "peraturan.bpk.go.id", FetchedAt: time.Unix(50, 0).UTC(), Status: "complete",
		Metadata: Page{Title: "Example", PDFs: []PDFLink{{URL: "https://peraturan.bpk.go.id/Download/7/main.pdf", Kind: "document"}}},
		PDFs:     []PDFReceipt{{URL: "https://peraturan.bpk.go.id/Download/7/main.pdf", Kind: "document", SHA256: pdfHash, Path: filepath.ToSlash(filepath.Join("blobs", pdfHash+".pdf")), Bytes: int64(len(examplePDF))}}}
	writeAuditRecord(t, root, url, record)
	writeAuditFile(t, root, "queue.txt", []byte(url+"\n"))

	missingBytes := []byte(examplePDF + "historical")
	missingHash := digest(missingBytes)
	historical := record
	historical.FetchedAt = time.Unix(40, 0).UTC()
	historical.PDFs = []PDFReceipt{{URL: missingURL, Kind: "document", SHA256: missingHash, Path: filepath.ToSlash(filepath.Join("blobs", missingHash+".pdf")), Bytes: int64(len(missingBytes))}}
	historical.Metadata.PDFs = []PDFLink{{URL: missingURL, Kind: "document"}}
	historicalRaw, err := json.Marshal(historical)
	if err != nil {
		t.Fatal(err)
	}
	key := digest([]byte(url))
	writeAuditFile(t, root, filepath.Join("observations", "0-"+key+".json"), historicalRaw)

	audit, err := AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Manifest.IntegrityValid || audit.Manifest.IssueCounts["missing_referenced_blob"] != 1 {
		t.Fatalf("missing historical blob passed audit: %+v", audit.Manifest.IssueCounts)
	}

	record.Metadata.PDFs = append(record.Metadata.PDFs, PDFLink{URL: missingURL, Kind: "document"})
	writeAuditRecord(t, root, url, record)
	audit, err = AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Manifest.IssueCounts["missing_required_pdf_receipt"] == 0 {
		t.Fatalf("complete record omitted a required primary PDF receipt: %+v", audit.Manifest.IssueCounts)
	}
}

func TestAuditAcquisitionRejectsIncompleteHTMLReference(t *testing.T) {
	root := t.TempDir()
	pdfHash := digest([]byte(examplePDF))
	writeAuditFile(t, root, filepath.Join("blobs", pdfHash+".pdf"), []byte(examplePDF))
	url := "https://peraturan.bpk.go.id/Details/8/example"
	record := Record{SchemaVersion: 1, ParserVersion: MetadataParserVersion, SourceURL: url, FinalURL: url,
		Portal: "peraturan.bpk.go.id", FetchedAt: time.Unix(60, 0).UTC(), Status: "complete", HTMLSHA256: digest([]byte("orphan html hash")),
		Metadata: Page{Title: "Example"}, PDFs: []PDFReceipt{{URL: "https://peraturan.bpk.go.id/Download/8/main.pdf", Kind: "document", SHA256: pdfHash, Path: filepath.ToSlash(filepath.Join("blobs", pdfHash+".pdf")), Bytes: int64(len(examplePDF))}}}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	key := digest([]byte(url))
	writeAuditFile(t, root, filepath.Join("records", key+".json"), raw)
	writeAuditFile(t, root, filepath.Join("observations", "1-"+key+".json"), raw)
	writeAuditFile(t, root, "queue.txt", []byte(url+"\n"))
	audit, err := AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Manifest.IntegrityValid || audit.Manifest.IssueCounts["incomplete_html_reference"] < 1 {
		t.Fatalf("incomplete HTML provenance passed: %+v", audit.Manifest.IssueCounts)
	}
}

func TestAuditAcquisitionAcceptsRecordedPreResponseFailureAndRejectsHistoricalStatus(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"records", "observations", "blobs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	url := "https://jdihn.go.id/doc/1"
	record := Record{SchemaVersion: 1, ParserVersion: MetadataParserVersion, SourceURL: url, Portal: "jdihn.go.id",
		FetchedAt: time.Unix(70, 0).UTC(), Status: "failed", Error: "HTTP 503 before response"}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	key := digest([]byte(url))
	writeAuditFile(t, root, filepath.Join("records", key+".json"), raw)
	observationPath := filepath.Join("observations", "1-"+key+".json")
	writeAuditFile(t, root, observationPath, raw)
	writeAuditFile(t, root, "queue.txt", []byte(url+"\n"))
	audit, err := AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Manifest.IntegrityValid || audit.Manifest.IssueCounts["source_acquisition_incomplete"] != 1 {
		t.Fatalf("recorded acquisition failure treated as corrupt: %+v", audit.Manifest.IssueCounts)
	}

	record.Status = "nonsense"
	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	writeAuditFile(t, root, observationPath, raw)
	audit, err = AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Manifest.IntegrityValid || audit.Manifest.IssueCounts["invalid_observation_status"] != 1 {
		t.Fatalf("invalid historical status passed: %+v", audit.Manifest.IssueCounts)
	}
}

func TestAuditAcquisitionReportsBrokenProvenanceWithoutHidingInventory(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"records", "observations", "blobs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orphanHash := digest([]byte(examplePDF))
	writeAuditFile(t, root, filepath.Join("blobs", orphanHash+".pdf"), []byte(examplePDF))
	missingPDF := []byte(examplePDF + "missing")
	missingHash := digest(missingPDF)
	sourceURL := "https://peraturan.bpk.go.id/Details/9/broken"
	record := Record{SchemaVersion: 1, ParserVersion: MetadataParserVersion, SourceURL: sourceURL, FinalURL: sourceURL,
		Portal: "peraturan.bpk.go.id", FetchedAt: time.Unix(30, 0).UTC(), Status: "complete",
		Metadata: Page{Title: "Broken"}, PDFs: []PDFReceipt{{URL: "https://peraturan.bpk.go.id/Download/9/broken.pdf", Kind: "document", SHA256: missingHash, Path: filepath.ToSlash(filepath.Join("blobs", missingHash+".pdf")), Bytes: int64(len(missingPDF))}}}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	writeAuditFile(t, root, filepath.Join("records", digest([]byte(sourceURL))+".json"), raw)
	writeAuditFile(t, root, "queue.txt", []byte(sourceURL+"\n"))

	audit, err := AuditAcquisition(context.Background(), AuditOptions{Root: root, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Manifest.IntegrityValid || audit.Manifest.IntegrityErrors < 2 {
		t.Fatalf("broken provenance passed: %+v", audit.Manifest)
	}
	if audit.Manifest.IssueCounts["missing_matching_observation"] != 1 || audit.Manifest.IssueCounts["missing_referenced_blob"] != 1 || audit.Manifest.IssueCounts["orphan_pdf_blob"] != 1 {
		t.Fatalf("missing expected findings: %+v", audit.Manifest.IssueCounts)
	}
	if len(audit.Records) != 1 || audit.Records[0].IntegrityStatus != "invalid" {
		t.Fatalf("record finding was hidden: %+v", audit.Records)
	}
}

func TestAuditAcquisitionHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"records", "observations", "blobs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AuditAcquisition(ctx, AuditOptions{Root: root, Workers: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func writeAuditRecord(t *testing.T, root, sourceURL string, record Record) {
	t.Helper()
	html := []byte("<html><body>source</body></html>")
	htmlHash := digest(html)
	record.HTMLSHA256 = htmlHash
	record.HTMLPath = filepath.ToSlash(filepath.Join("pages", htmlHash+".html"))
	writeAuditFile(t, root, record.HTMLPath, html)
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	key := digest([]byte(sourceURL))
	writeAuditFile(t, root, filepath.Join("records", key+".json"), raw)
	writeAuditFile(t, root, filepath.Join("observations", "1-"+key+".json"), raw)
}

func writeAuditFile(t *testing.T, root, relative string, raw []byte) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
