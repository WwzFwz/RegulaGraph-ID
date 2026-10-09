// Verifies acquisition import with real confined file stores and a recording registry.
// Covers corruption, replay, cancellation, byte bounds, mirror provenance and corpus isolation;
// tiny PDF-signature fixtures do not prove parser/model quality or benchmark acceptance.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/ingestion/sources"
)

type importRegistry struct {
	refs []*pb.ArtifactRef
	fail bool
}

func TestAcquisitionImportPartialCopyAndMetadataBudget(t *testing.T) {
	request, record, root := importFixture(t)
	second := record.PDFs[0]
	data := []byte("%PDF-1.7\nsecond file\n%%EOF")
	digest := sha256.Sum256(data)
	second.SHA256 = hex.EncodeToString(digest[:])
	second.Path = "blobs/" + second.SHA256 + ".pdf"
	second.URL = "https://peraturan.bpk.go.id/Download/2/second.pdf"
	second.Bytes = int64(len(data))
	record.PDFs = append(record.PDFs, second)
	prepared, err := PrepareAcquisitionImport(request, record)
	if err != nil {
		t.Fatal(err)
	}
	source, err := storage.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	dest, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	registry := &importRegistry{}
	if err := prepared.Import(context.Background(), source, dest, registry); err == nil || len(registry.refs) != 0 {
		t.Fatal("missing second PDF registered partial record")
	}
	if err := os.WriteFile(filepath.Join(root, second.Path), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Import(context.Background(), source, dest, registry); err != nil || len(registry.refs) != 2 {
		t.Fatalf("retry failed: %v", err)
	}
	record.Metadata.PDFs = []sources.PDFLink{{URL: "https://peraturan.bpk.go.id/Download/missing.pdf"}}
	if _, err := PrepareAcquisitionImport(request, record); err == nil {
		t.Fatal("incomplete required PDF set accepted")
	}
	record.Metadata.PDFs = nil
	record.Metadata.Fields = map[string][]string{"large": {strings.Repeat("x", 256<<10)}}
	for len(record.PDFs) < 64 {
		record.PDFs = append(record.PDFs, second)
	}
	if _, err := PrepareAcquisitionImport(request, record); err == nil || !strings.Contains(err.Error(), "metadata exceeds budget") {
		t.Fatalf("metadata amplification did not fail preflight: %v", err)
	}
}

func (r *importRegistry) RegisterArtifact(_ context.Context, _ string, ref *pb.ArtifactRef) error {
	if r.fail {
		return errors.New("registry unavailable")
	}
	r.refs = append(r.refs, proto.Clone(ref).(*pb.ArtifactRef))
	return nil
}

func importFixture(t *testing.T) (*pb.IngestionRequest, sources.Record, string) {
	t.Helper()
	data := []byte("%PDF-1.7\nfixture body\n%%EOF")
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blobs", hash+".pdf"), data, 0600); err != nil {
		t.Fatal(err)
	}
	request := &pb.IngestionRequest{CorpusId: "corpus:import", Operation: pb.JobOperation_JOB_OPERATION_INGEST,
		IdempotencyKey: "import:fixture", ConfigManifest: &pb.ProducerManifest{Software: "fixture", Build: "test",
			SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: hash}}}
	record := sources.Record{SchemaVersion: 1, ParserVersion: sources.MetadataParserVersion, Status: "complete",
		SourceURL: "https://peraturan.bpk.go.id/Details/1/example", Portal: "peraturan.bpk.go.id",
		FetchedAt: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), Metadata: sources.Page{Title: "Fixture regulation"},
		PDFs: []sources.PDFReceipt{{URL: "https://peraturan.bpk.go.id/Download/1/main.pdf", Kind: "document",
			SHA256: hash, Bytes: int64(len(data)), Path: "blobs/" + hash + ".pdf", ContentType: "application/pdf"}}}
	return request, record, root
}

func TestAcquisitionImportReplayAndIsolation(t *testing.T) {
	request, record, root := importFixture(t)
	mirror := record.PDFs[0]
	mirror.URL = "https://peraturan.bpk.go.id/Download/2/mirror.pdf"
	record.PDFs = append(record.PDFs, mirror)
	prepared, err := PrepareAcquisitionImport(request, record)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Request().Sources) != 1 || len(prepared.Request().Observations) != 2 {
		t.Fatal("lost mirror provenance or blob dedup")
	}
	other := proto.Clone(request).(*pb.IngestionRequest)
	other.CorpusId = "corpus:other"
	second, err := PrepareAcquisitionImport(other, record)
	if err != nil {
		t.Fatal(err)
	}
	firstRef, secondRef := prepared.Request().Sources[0].GetBlob(), second.Request().Sources[0].GetBlob()
	if firstRef.ArtifactId == secondRef.ArtifactId || firstRef.StorageKey == secondRef.StorageKey || !proto.Equal(firstRef.ContentHash, secondRef.ContentHash) {
		t.Fatal("corpus ownership or source hash broken")
	}
	mutated := prepared.Request()
	mutated.Sources[0].GetBlob().StorageKey = "changed"
	if prepared.Request().Sources[0].GetBlob().StorageKey == "changed" {
		t.Fatal("mutable preparation leaked")
	}
	source, err := storage.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	dest, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	registry := &importRegistry{fail: true}
	if err := prepared.Import(context.Background(), source, dest, registry); err == nil {
		t.Fatal("registration failure hidden")
	}
	registry.fail = false
	for range 2 {
		if err := prepared.Import(context.Background(), source, dest, registry); err != nil {
			t.Fatal(err)
		}
	}
	if len(registry.refs) != 2 || !proto.Equal(registry.refs[0], registry.refs[1]) {
		t.Fatal("replay changed reference")
	}
	// Corrupt collector input must be rejected even when a valid destination already exists.
	if err := os.WriteFile(filepath.Join(root, record.PDFs[0].Path), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Import(context.Background(), source, dest, registry); err == nil || len(registry.refs) != 2 {
		t.Fatal("corrupt input admitted on reuse")
	}
}

func TestAcquisitionImportRejectsInvalidAndCancelled(t *testing.T) {
	request, record, root := importFixture(t)
	prepared, err := PrepareAcquisitionImport(request, record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAcquisitionImport(prepared.Request(), record); err == nil {
		t.Fatal("overwrote supplied sources")
	}
	record.PDFs[0].ContentType = strings.Repeat("x", 8193)
	if _, err := PrepareAcquisitionImport(request, record); err == nil || !strings.Contains(err.Error(), "field exceeds") {
		t.Fatalf("oversized receipt not rejected before sort: %v", err)
	}
	record.PDFs[0].ContentType = "application/pdf"
	record.PDFs[0].Bytes = int64(maximumAcquisitionImportBytes) + 1
	if _, err := PrepareAcquisitionImport(request, record); err == nil {
		t.Fatal("unbounded import accepted")
	}
	record.PDFs[0].Path = "../escape.pdf"
	if _, err := PrepareAcquisitionImport(request, record); err == nil {
		t.Fatal("traversal accepted")
	}
	source, err := storage.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	dest, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	registry := &importRegistry{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := prepared.Import(ctx, source, dest, registry); !errors.Is(err, context.Canceled) || len(registry.refs) != 0 {
		t.Fatal("cancelled import mutated registry")
	}
}
