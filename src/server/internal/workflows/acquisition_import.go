// Bridges completed collector records into durable ingestion without changing source bytes.
// Preparation preserves portal assertions and gives artifact metadata corpus-scoped identities;
// import streams verified PDFs to shared storage before registering any reference or enqueueing.
// Retry may leave immutable unreferenced blobs/registrations, never a partially submitted job.
// Bound work by PDF count and aggregate bytes; measure import throughput and cancellation latency
// against configs/benchmark-targets.yaml (REQUIRED_UNMEASURED). No model-quality claim is implied.
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/ingestion/sources"
)

const maximumAcquisitionImportBytes uint64 = 256 << 20
const maximumAcquisitionImportPDFs = 64

type AcquisitionImport struct {
	request *pb.IngestionRequest
	sources []*pb.ArtifactRef
}

type AcquisitionImportReader interface {
	OpenVerified(context.Context, *pb.ArtifactRef) (*os.File, error)
}
type AcquisitionImportWriter interface {
	Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error)
}
type AcquisitionImportRegistry interface {
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
}

// PrepareAcquisitionImport accepts an empty-source request template, never silently
// overwriting user-supplied sources or observations. All receipt bounds precede file I/O.
func PrepareAcquisitionImport(template *pb.IngestionRequest, record sources.Record) (*AcquisitionImport, error) {
	if template == nil || len(template.Sources) != 0 || len(template.Observations) != 0 ||
		template.Operation != pb.JobOperation_JOB_OPERATION_INGEST {
		return nil, errors.New("acquisition import requires an INGEST template without sources or observations")
	}
	if len(record.PDFs) == 0 || len(record.PDFs) > maximumAcquisitionImportPDFs {
		return nil, errors.New("acquisition import requires 1..64 PDF receipts")
	}
	if err := acquisitionMetadataBudget(template, record); err != nil {
		return nil, err
	}
	if record.Error != "" {
		return nil, errors.New("acquisition record reports an error")
	}
	receipts := make(map[string]bool, len(record.PDFs))
	for _, receipt := range record.PDFs {
		receipts[receipt.URL] = receipt.Error == ""
	}
	for _, link := range record.Metadata.PDFs {
		if !receipts[link.URL] {
			return nil, errors.New("acquisition record lacks a required PDF receipt")
		}
	}
	handoff, err := sources.BuildIngestionHandoff(template.CorpusId, record)
	if err != nil {
		return nil, err
	}
	var total uint64
	originals := make([]*pb.ArtifactRef, 0, len(handoff.Sources))
	for _, source := range handoff.Sources {
		ref := source.GetBlob()
		originals = append(originals, proto.Clone(ref).(*pb.ArtifactRef))
		if ref.ByteSize > maximumAcquisitionImportBytes-total {
			return nil, errors.New("acquisition import exceeds 256 MiB; select a smaller complete record")
		}
		total += ref.ByteSize
		// PostgreSQL artifact IDs and storage keys are globally unique, while
		// ownership is corpus-specific. Source-blob identity remains content-addressed.
		digest := sha256.Sum256([]byte(template.CorpusId + "\x00" + ref.ContentHash.Sha256))
		ref.ArtifactId = "source-import:" + hex.EncodeToString(digest[:])
		ref.StorageKey = "imported/" + hex.EncodeToString(digest[:]) + ".pdf"
	}
	request := proto.Clone(template).(*pb.IngestionRequest)
	request.Sources, request.Observations = handoff.Sources, handoff.Observations
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if _, err := parseObservations(request); err != nil {
		return nil, err
	}
	return &AcquisitionImport{request: request, sources: originals}, nil
}

// Conservatively account for strings and per-value allocations before metadata is
// repeated for every observation. This is an admission bound, not a heap benchmark.
func acquisitionMetadataBudget(template *pb.IngestionRequest, record sources.Record) error {
	const limit = 8 << 20
	if proto.Size(template) > limit {
		return errors.New("acquisition template exceeds 8 MiB")
	}
	remaining := limit / len(record.PDFs)
	charge := func(value string) bool {
		if len(value) > remaining || 256 > remaining-len(value) {
			return false
		}
		remaining -= len(value) + 256
		return true
	}
	for _, value := range []string{record.SourceURL, record.FinalURL, record.ParserVersion, record.Metadata.Title} {
		if !charge(value) {
			return errors.New("expanded acquisition metadata exceeds budget")
		}
	}
	for field, values := range record.Metadata.Fields {
		if !charge(field) {
			return errors.New("expanded acquisition metadata exceeds budget")
		}
		for _, value := range values {
			if !charge(field) || !charge(value) {
				return errors.New("expanded acquisition metadata exceeds budget")
			}
		}
	}
	for _, receipt := range record.PDFs {
		if receipt.Error != "" {
			return errors.New("acquisition PDF receipt reports an error")
		}
		for _, value := range []string{receipt.URL, receipt.FinalURL, receipt.Kind, receipt.Label, receipt.Path, receipt.ETag, receipt.LastModified, receipt.SHA256, receipt.ContentType} {
			if len(value) > 8192 {
				return errors.New("acquisition receipt field exceeds 8 KiB")
			}
		}
	}
	return nil
}

func (p *AcquisitionImport) Request() *pb.IngestionRequest {
	return proto.Clone(p.request).(*pb.IngestionRequest)
}

// Import verifies every source even on destination reuse. It registers only after
// all copies have succeeded. Registration failure is safely replayable; caller must
// not submit until this method succeeds. Storage roots are supplied by the operator.
func (p *AcquisitionImport) Import(ctx context.Context, source AcquisitionImportReader,
	destination AcquisitionImportWriter, registry AcquisitionImportRegistry) error {
	if p == nil || p.request == nil || source == nil || destination == nil || registry == nil {
		return errors.New("prepared acquisition import and storage ports are required")
	}
	for index, locator := range p.request.Sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := locator.GetBlob()
		file, err := source.OpenVerified(ctx, p.sources[index])
		if err != nil {
			return fmt.Errorf("verify acquired PDF: %w", err)
		}
		// The collector verifies the PDF signature; recheck it at the import boundary.
		var signature [512]byte
		n, readErr := io.ReadFull(file, signature[:])
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			err = readErr
		}
		if err == nil && !bytes.HasPrefix(bytes.TrimSpace(signature[:n]), []byte("%PDF-")) {
			err = errors.New("acquired artifact lacks PDF signature")
		}
		if err == nil {
			_, err = file.Seek(0, io.SeekStart)
		}
		if err == nil {
			_, err = destination.Put(ctx, ref, io.LimitReader(file, int64(ref.ByteSize)+1))
		}
		closeErr := file.Close()
		if err != nil {
			return fmt.Errorf("copy acquired PDF: %w", err)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, locator := range p.request.Sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := registry.RegisterArtifact(ctx, p.request.CorpusId, locator.GetBlob()); err != nil {
			return fmt.Errorf("register acquired PDF: %w", err)
		}
	}
	return nil
}
