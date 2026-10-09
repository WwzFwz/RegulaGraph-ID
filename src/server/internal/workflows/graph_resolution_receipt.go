// Authenticates original RESOLVE bytes against a successful source checkpoint and
// the immutable semantic intent/decision ledger before graph preparation. Nonempty
// batches must reconstruct exactly from the committed operation; empty extraction
// follows the explicit no-operation path. No model call, registry write or revision
// refresh occurs. Reads are registered, hash-checked and aggregate bounded at 16MiB.
// Measure read/decode/ledger p95 and rejection rate under benchmark-targets.yaml;
// this proves historical decision provenance, not current dependency freshness.
package workflows

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphResolutionReceiptStore interface {
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	VerifyGraphAssemblySourceCheckpoint(context.Context, string, string, string, *pb.ArtifactRef) error
	LoadSemanticResolutionIntent(context.Context, string, string) (domain.SemanticResolutionIntent, error)
	ReadCommittedSemanticResolution(context.Context, domain.SemanticRegistryInputs, *pb.RegistryResolveRequest, []domain.ReviewedLink, int, int) (*pb.RegistryResolveResponse, error)
}

func ReadGraphResolutionReceipt(ctx context.Context, store GraphResolutionReceiptStore, reader DocumentArtifactReader,
	corpus, job, checkpoint string, extractionRef, resolutionRef *pb.ArtifactRef, maximumEdges, maximumCandidates int) (*pb.ResolutionBatch, error) {
	if ctx == nil || store == nil || reader == nil || corpus == "" || job == "" || checkpoint == "" || maximumEdges <= 0 || maximumEdges > domain.DefaultWireLimits.MaxItems || maximumCandidates <= 0 || maximumCandidates > domain.DefaultWireLimits.MaxItems {
		return nil, errors.New("bounded graph resolution reader required")
	}
	for _, ref := range []*pb.ArtifactRef{extractionRef, resolutionRef} {
		if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	if extractionRef.ArtifactId == resolutionRef.ArtifactId ||
		(extractionRef.MediaType != domain.ExtractionBatchMediaType && extractionRef.MediaType != "application/x-protobuf") ||
		(resolutionRef.MediaType != "application/x-protobuf" && resolutionRef.MediaType != "application/x-protobuf; message=regulagraph.v1.ResolutionBatch") {
		return nil, errors.New("graph resolution artifact roles mismatch")
	}
	if err := store.VerifyGraphAssemblySourceCheckpoint(ctx, corpus, job, checkpoint, resolutionRef); err != nil {
		return nil, err
	}
	remaining := uint64(domain.DefaultWireLimits.MaxBytes)
	extractionBytes, err := readGraphPreparationArtifact(ctx, store, reader, corpus, extractionRef, &remaining)
	if err != nil {
		return nil, err
	}
	resolutionBytes, err := readGraphPreparationArtifact(ctx, store, reader, corpus, resolutionRef, &remaining)
	if err != nil {
		return nil, err
	}
	extraction, resolution := new(pb.ExtractionBatch), new(pb.ResolutionBatch)
	if err = domain.DecodeWire(extractionBytes, extraction, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if err = domain.DecodeWire(resolutionBytes, resolution, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if extraction.Meta.CorpusId != corpus || resolution.Meta.CorpusId != corpus {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateGraphResolutionSource(extraction, extractionRef, resolution, maximumEdges); err != nil {
		return nil, err
	}
	if len(extraction.Mentions) == 0 {
		// Closure validation accounts for zero proposals/decisions; no ledger is invented.
		return resolution, nil
	}
	intent, err := store.LoadSemanticResolutionIntent(ctx, corpus, job)
	if err != nil {
		return nil, err
	}
	if intent.Request == nil || intent.Preview == nil || !proto.Equal(intent.Preview, resolution) {
		return nil, fmt.Errorf("graph source differs from durable resolution intent: %w", domain.ErrPersistentIntegrity)
	}
	candidateBytes, err := readGraphPreparationArtifact(ctx, store, reader, corpus, intent.CandidateRef, &remaining)
	if err != nil {
		return nil, err
	}
	candidates := new(pb.RegistryCandidateBatch)
	if err = domain.DecodeWire(candidateBytes, candidates, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	input := domain.SemanticRegistryInputs{SourceRef: extractionRef, SourceBytes: extractionBytes, CandidateRef: intent.CandidateRef, CandidateBytes: candidateBytes}
	committed, err := store.ReadCommittedSemanticResolution(ctx, input, intent.Request, intent.Approvals, maximumEdges, maximumCandidates)
	if err != nil {
		return nil, err
	}
	reconstructed, err := domain.AssembleResolutionBatchFromReceipt(extraction, extractionRef, candidates, intent.CandidateRef,
		intent.Request, committed, resolution.ModelManifest, resolution.Dependencies.ProducerManifest, resolution.TokenUsage,
		resolution.Meta.RecordId, maximumEdges, maximumCandidates)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(reconstructed, resolution) {
		return nil, fmt.Errorf("graph source differs from committed decisions: %w", domain.ErrPersistentIntegrity)
	}
	return resolution, nil
}

type graphPreparationArtifactStore interface {
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
}

func readGraphPreparationArtifact(ctx context.Context, store graphPreparationArtifactStore, reader DocumentArtifactReader,
	corpus string, ref *pb.ArtifactRef, remaining *uint64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if ref.SchemaVersion != 1 || ref.ByteSize == 0 || ref.ByteSize > *remaining {
		return nil, errors.New("graph preparation input byte budget exceeded")
	}
	*remaining -= ref.ByteSize
	registered, err := store.LoadArtifact(ctx, corpus, ref.ArtifactId)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(registered, ref) {
		return nil, domain.ErrPersistentIntegrity
	}
	raw, err := reader.ReadVerified(ctx, ref, ref.ByteSize)
	if err != nil {
		return nil, err
	}
	if uint64(len(raw)) != ref.ByteSize || fmt.Sprintf("%x", sha256.Sum256(raw)) != ref.ContentHash.Sha256 {
		return nil, domain.ErrPersistentIntegrity
	}
	return raw, nil
}
