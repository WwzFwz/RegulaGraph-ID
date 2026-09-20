// Verifies acquisition-to-ingestion provenance binding without treating portal metadata as canonical truth.
// Fixtures cover deterministic replay, duplicate byte reuse, metadata changes, and malformed receipts.
package sources

import (
	"strings"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestBuildIngestionHandoffBindsMetadataToPDFBytes(t *testing.T) {
	record := handoffRecord()
	second := record.PDFs[0]
	second.URL = "https://peraturan.bpk.go.id/Download/1/mirror.pdf"
	second.FinalURL = second.URL
	second.Label = "mirror.pdf"
	record.PDFs = append(record.PDFs, second)

	first, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	secondRun, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Sources) != 1 || len(first.Observations) != 2 {
		t.Fatalf("expected one deduplicated blob and two observations: %+v", first)
	}
	if first.Sources[0].PortalId != "bpk" || first.Sources[0].GetBlob().ArtifactId != "source-artifact:"+record.PDFs[0].SHA256 {
		t.Fatalf("unexpected source locator: %v", first.Sources[0])
	}
	for index, observation := range first.Observations {
		if observation.GetSourceBlobId() != "source-blob:"+record.PDFs[0].SHA256 || observation.GetMetadataHash() == nil ||
			observation.GetMeta().GetRecordId() != secondRun.Observations[index].GetMeta().GetRecordId() {
			t.Fatalf("observation is not deterministically bound: %v", observation)
		}
	}
	request := &pb.IngestionRequest{
		CorpusId: "corpus:fixture", Sources: first.Sources, Observations: first.Observations,
		Operation: pb.JobOperation_JOB_OPERATION_INGEST, IdempotencyKey: "ingest:fixture",
		ConfigManifest: &pb.ProducerManifest{Software: "test", Build: "test", SchemaVersion: 1,
			ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("c", 64)}},
	}
	if err = domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		t.Fatalf("handoff does not satisfy wire contract: %v", err)
	}
}

func TestBuildIngestionHandoffKeepsMetadataChangesObservable(t *testing.T) {
	record := handoffRecord()
	before, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	record.Metadata.Fields["portal_status"] = []string{"Tidak Berlaku"}
	after, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	if before.Sources[0].GetBlob().GetContentHash().GetSha256() != after.Sources[0].GetBlob().GetContentHash().GetSha256() ||
		before.Observations[0].GetMetadataHash().GetSha256() == after.Observations[0].GetMetadataHash().GetSha256() {
		t.Fatal("metadata change either changed blob identity or was hidden")
	}
}

func TestBuildIngestionHandoffCanonicalizesMetadataBeforeHashing(t *testing.T) {
	record := handoffRecord()
	record.Metadata.Fields["issuer"] = []string{" Indonesia ", "Kementerian", "Indonesia", " Kementerian "}
	first, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	record.Metadata.Fields["issuer"] = []string{"Kementerian", "Indonesia"}
	second, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	if first.Observations[0].GetMetadataHash().GetSha256() != second.Observations[0].GetMetadataHash().GetSha256() {
		t.Fatal("equivalent canonical metadata produced different hashes")
	}
	var issuers []string
	for _, value := range first.Observations[0].PortalMetadata {
		if value.GetName() == "issuer" {
			issuers = append(issuers, value.GetText())
		}
	}
	if strings.Join(issuers, ",") != "Indonesia,Kementerian" {
		t.Fatalf("metadata values were not normalized, sorted, and deduplicated: %v", issuers)
	}
}

func TestBuildIngestionHandoffHashesReceiptMetadata(t *testing.T) {
	record := handoffRecord()
	before, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	record.PDFs[0].Kind = "attachment"
	record.PDFs[0].Label = "lampiran.pdf"
	after, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	if before.Observations[0].GetMetadataHash().GetSha256() == after.Observations[0].GetMetadataHash().GetSha256() {
		t.Fatal("emitted receipt metadata changed without changing metadata hash")
	}
}

func TestBuildIngestionHandoffRejectsConflictingDuplicateBlobDescriptors(t *testing.T) {
	record := handoffRecord()
	conflict := record.PDFs[0]
	conflict.URL = "https://peraturan.bpk.go.id/Download/1/conflict.pdf"
	conflict.Bytes++
	record.PDFs = append(record.PDFs, conflict)
	if _, err := BuildIngestionHandoff("corpus:fixture", record); err == nil {
		t.Fatal("same content hash with conflicting byte size was accepted")
	}
}

func TestBuildIngestionHandoffOrdersObservationsByCompleteReceiptIdentity(t *testing.T) {
	record := handoffRecord()
	later := record.PDFs[0]
	later.FetchedAt = later.FetchedAt.Add(time.Second)
	record.PDFs = append(record.PDFs, later)
	forward, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	record.PDFs[0], record.PDFs[1] = record.PDFs[1], record.PDFs[0]
	reversed, err := BuildIngestionHandoff("corpus:fixture", record)
	if err != nil {
		t.Fatal(err)
	}
	for index := range forward.Observations {
		if forward.Observations[index].GetMeta().GetRecordId() != reversed.Observations[index].GetMeta().GetRecordId() {
			t.Fatal("receipt input order changed observation output order")
		}
	}
}

func TestBuildIngestionHandoffRejectsUnpublishableRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "partial record", mutate: func(record *Record) { record.Status = "partial" }},
		{name: "unsupported portal", mutate: func(record *Record) { record.Portal = "example.invalid" }},
		{name: "untrusted resolved detail URL", mutate: func(record *Record) { record.FinalURL = "https://attacker.invalid/forged" }},
		{name: "failed receipt", mutate: func(record *Record) { record.PDFs[0].Error = "truncated" }},
		{name: "wrong storage key", mutate: func(record *Record) { record.PDFs[0].Path = "blobs/other.pdf" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := handoffRecord()
			test.mutate(&record)
			if _, err := BuildIngestionHandoff("corpus:fixture", record); err == nil {
				t.Fatal("invalid acquisition record was accepted")
			}
		})
	}
}

func handoffRecord() Record {
	hash := strings.Repeat("a", 64)
	fetched := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	return Record{
		SchemaVersion: 1, ParserVersion: MetadataParserVersion,
		SourceURL: "https://peraturan.bpk.go.id/Details/1/example", FinalURL: "https://peraturan.bpk.go.id/Details/1/example",
		Portal: "peraturan.bpk.go.id", FetchedAt: fetched, HTMLSHA256: strings.Repeat("b", 64), Status: "complete",
		Metadata: Page{Title: "Undang-Undang Nomor 1 Tahun 2026", Fields: map[string][]string{
			"regulation_type": {"Undang-Undang"}, "number": {"1"}, "year": {"2026"}, "issuer": {"Indonesia"},
		}},
		PDFs: []PDFReceipt{{
			URL: "https://peraturan.bpk.go.id/Download/1/main.pdf", FinalURL: "https://peraturan.bpk.go.id/Download/1/main.pdf",
			Label: "main.pdf", Kind: "document", SHA256: hash, Path: "blobs/" + hash + ".pdf", Bytes: 123,
			ContentType: "application/pdf", ETag: "etag", LastModified: "Mon, 21 Sep 2026 01:02:03 GMT", FetchedAt: fetched,
		}},
	}
}
