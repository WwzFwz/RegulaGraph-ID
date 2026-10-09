// Composes complete published-source discovery, historical resolution admission,
// fenced envelope binding and deterministic ASSEMBLE plans. All sources are
// preflighted before reserving a target; no missing/unresolved source is omitted.
// Scheduling remains a separate atomic storage admission, so interrupted artifact
// writes can be retried without partially scheduled children. Input metadata is
// bounded at 64MiB and per-source worker limits remain enforced downstream.
// Measure read/replay/SQL p95 and bytes under benchmark-targets.yaml; this workflow
// proves provenance and readiness, not extraction accuracy or release performance.
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

type GraphPreparationStore interface {
	GraphAssemblyPreparationStore
	GraphSourceBindingStore
	LoadPublishedGraphSources(context.Context, domain.SnapshotPin, string) ([]domain.IndexSourceBinding, error)
	LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error)
	ReservePublication(context.Context, string, string, string, string, string) (domain.PublicationReservation, error)
	BindPublicationRegistry(context.Context, string, string, uint64, uint64) error
}

type GraphPreparationConfig struct {
	CorpusID, PublicationID, SnapshotID, BaseSnapshotID, AuthScope string
	Producer                                                       *pb.ProducerManifest
	OntologyHash                                                   *pb.ContentHash
	MaximumReferences, MaximumCandidates                           int
}

// PrepareGraphInventory returns plans, not a scheduling capability. The caller
// must restore input bytes, obtain PrepareGraphJobAdmission and invoke Schedule.
func PrepareGraphInventory(ctx context.Context, store GraphPreparationStore, reader DocumentArtifactReader, writer GraphSourceArtifactWriter,
	pin domain.SnapshotPin, cfg GraphPreparationConfig) (domain.GraphJobInventory, error) {
	var empty domain.GraphJobInventory
	if ctx == nil || store == nil || reader == nil || writer == nil || cfg.MaximumReferences <= 0 || cfg.MaximumReferences > domain.DefaultWireLimits.MaxItems ||
		cfg.MaximumCandidates <= 0 || cfg.MaximumCandidates > domain.DefaultWireLimits.MaxItems || pin.CorpusID != cfg.CorpusID || pin.SnapshotID != cfg.BaseSnapshotID || pin.ExpiresAt.IsZero() {
		return empty, errors.New("bounded graph preparation dependencies and exact base required")
	}
	for _, id := range []string{cfg.PublicationID, cfg.SnapshotID, cfg.BaseSnapshotID, cfg.AuthScope} {
		if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.CorpusID, RecordId: id}, domain.DefaultWireLimits); err != nil {
			return empty, err
		}
	}
	for _, m := range []proto.Message{cfg.Producer, cfg.OntologyHash} {
		if err := domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
			return empty, err
		}
	}
	if cfg.SnapshotID == cfg.BaseSnapshotID {
		return empty, errors.New("graph target must differ from base")
	}
	ctx, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	sources, err := store.LoadPublishedGraphSources(ctx, pin, cfg.AuthScope)
	if err != nil {
		return empty, err
	}
	if len(sources) == 0 || len(sources) > 256 {
		return empty, errors.New("complete bounded graph source selection required")
	}
	requests := make([]domain.GraphSourceBinding, 0, len(sources))
	remaining := uint64(64 << 20)
	var revision uint64
	for i, source := range sources {
		if err = domain.ValidateIndexSourceBinding(source); err != nil {
			return empty, err
		}
		if source.AuthScope != cfg.AuthScope || source.Snapshot.CorpusId != cfg.CorpusID || source.Snapshot.SnapshotId != cfg.BaseSnapshotID ||
			source.Snapshot.Sequence != pin.Sequence || i > 0 && sources[i-1].SourceJobID >= source.SourceJobID {
			return empty, domain.ErrPersistentIntegrity
		}
		cp, e := store.LoadLatestCheckpoint(ctx, source.SourceJobID)
		if e != nil {
			return empty, fmt.Errorf("source %s checkpoint: %w", source.SourceJobID, e)
		}
		if domain.ValidateWire(cp, domain.DefaultWireLimits) != nil || cp.GetMeta().GetCorpusId() != cfg.CorpusID || cp.JobId != source.SourceJobID ||
			cp.Stage != pb.JobStage_JOB_STAGE_RESOLVE || cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || len(cp.CompletedBatchKeys) != 1 || len(cp.ArtifactHashes) != 1 {
			return empty, fmt.Errorf("source %s requires successful RESOLVE", source.SourceJobID)
		}
		ref, e := store.LoadArtifact(ctx, cfg.CorpusID, cp.CompletedBatchKeys[0])
		if e != nil {
			return empty, e
		}
		if domain.ValidateWire(ref, domain.DefaultWireLimits) != nil || !proto.Equal(ref.ContentHash, cp.ArtifactHashes[0]) {
			return empty, domain.ErrPersistentIntegrity
		}
		if e = store.VerifyGraphAssemblySourceCheckpoint(ctx, cfg.CorpusID, source.SourceJobID, cp.Meta.RecordId, ref); e != nil {
			return empty, e
		}
		raw, e := readGraphPreparationArtifact(ctx, store, reader, cfg.CorpusID, ref, &remaining)
		if e != nil {
			return empty, e
		}
		resolution := new(pb.ResolutionBatch)
		if e = domain.DecodeWire(raw, resolution, domain.DefaultWireLimits); e != nil {
			return empty, e
		}
		if i == 0 {
			revision = resolution.RegistryRevision
		}
		if revision == 0 || resolution.RegistryRevision != revision {
			return empty, fmt.Errorf("source %s needs cross-revision reaffirmation: %w", source.SourceJobID, domain.ErrResolutionReplan)
		}
		if len(resolution.Decisions) > 0 {
			intent, e := store.LoadSemanticResolutionIntent(ctx, cfg.CorpusID, source.SourceJobID)
			if e != nil {
				return empty, e
			}
			if domain.ValidateWire(intent.CandidateRef, domain.DefaultWireLimits) != nil || intent.CandidateRef.ByteSize > remaining {
				return empty, errors.New("graph preparation candidate budget exceeded")
			}
			remaining -= intent.CandidateRef.ByteSize
		}
		// Reserve aggregate budget for subsequent authenticated source reads.
		for _, input := range []*pb.ArtifactRef{source.Original, source.Bound, resolution.SourceExtractionBatch} {
			if domain.ValidateWire(input, domain.DefaultWireLimits) != nil || input.ByteSize > remaining {
				return empty, errors.New("graph preparation source budget exceeded")
			}
			remaining -= input.ByteSize
		}
		checked, e := ReadGraphResolutionForAssembly(ctx, store, reader, cfg.CorpusID, source.SourceJobID, cp.Meta.RecordId,
			resolution.SourceExtractionBatch, ref, revision, cfg.MaximumReferences, cfg.MaximumCandidates)
		if e != nil {
			return empty, fmt.Errorf("source %s resolution admission: %w", source.SourceJobID, e)
		}
		if _, e = domain.GraphAssemblyCanonicalSelection(checked); e != nil {
			return empty, fmt.Errorf("source %s: %w", source.SourceJobID, e)
		}
		requests = append(requests, domain.GraphSourceBinding{Policy: domain.GraphSourceEnvelopePolicy, PublicationID: cfg.PublicationID,
			RegistryRevision: revision, SourceCheckpointID: cp.Meta.RecordId, Source: source, OriginalExtraction: resolution.SourceExtractionBatch, OriginalResolution: ref})
	}
	target, err := store.ReservePublication(ctx, cfg.PublicationID, "", cfg.CorpusID, cfg.SnapshotID, cfg.BaseSnapshotID)
	if err != nil {
		return empty, err
	}
	if target.CorpusID != cfg.CorpusID || target.PublicationID != cfg.PublicationID || target.SnapshotID != cfg.SnapshotID || target.ParentSnapshotID != cfg.BaseSnapshotID ||
		target.Fence == 0 || target.Sequence <= pin.Sequence || (target.State != pb.SnapshotState_SNAPSHOT_STATE_STAGING && target.State != pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) {
		return empty, errors.New("graph publication reservation differs or is not open")
	}
	if err = store.BindPublicationRegistry(ctx, cfg.PublicationID, cfg.CorpusID, target.Fence, revision); err != nil {
		return empty, err
	}
	var in domain.GraphJobInventory
	for _, request := range requests {
		request.Fence, request.TargetSequence = target.Fence, target.Sequence
		if _, err = BindGraphSource(ctx, store, reader, writer, pin, request, cfg.MaximumReferences); err != nil {
			return empty, err
		}
		prepared, e := PrepareGraphAssembly(ctx, store, reader, writer, pin, GraphAssemblyPreparationConfig{CorpusID: cfg.CorpusID,
			PublicationID: cfg.PublicationID, SourceJobID: request.Source.SourceJobID, Producer: cfg.Producer, OntologyHash: cfg.OntologyHash,
			MaximumReferences: cfg.MaximumReferences, MaximumCandidates: cfg.MaximumCandidates})
		if e != nil {
			return empty, e
		}
		id := fmt.Sprintf("job:assembly:%x", sha256.Sum256([]byte(prepared.Plan.Meta.RecordId)))
		in.Assignments = append(in.Assignments, domain.GraphJobAssignment{JobID: id, SourceJobID: request.Source.SourceJobID, Plan: prepared.Plan, Reference: prepared.Reference})
	}
	if err = domain.ValidateGraphJobInventory(in); err != nil {
		return empty, err
	}
	return in, nil
}
