// Prepares immutable ASSEMBLE plan/view artifacts from a durable graph source
// receipt and authenticated historical RESOLVE decisions. Canonical selection is
// exact, IDs deterministic and required source text fits the worker's 16MiB budget.
// This step creates no jobs: admission must still prove dependency freshness and
// repeat live authority in the scheduling transaction. Cross-revision decisions
// remain rejected until explicit reaffirmation exists. Record SQL/read/hash/write
// p95, bytes and replay cost under benchmark-targets.yaml (REQUIRED_UNMEASURED).
package workflows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphAssemblyPreparationStore interface {
	GraphResolutionReceiptStore
	LoadGraphSourceBinding(context.Context, string, string, string) (domain.GraphSourceBinding, error)
	VerifyPublishedGraphSourceBinding(context.Context, domain.SnapshotPin, domain.IndexSourceBinding) error
	VerifyGraphAssemblyPublication(context.Context, *pb.GraphAssemblyPlan) error
	VerifyDocumentRegistryView(context.Context, string, *pb.ArtifactRef, []byte, uint64, int) error
	ExportRegistryEntityView(context.Context, domain.RegistryEntityExport) (*pb.RegistryEntityView, error)
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
	EnsureArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
}

type GraphAssemblyPreparationConfig struct {
	CorpusID, PublicationID, SourceJobID string
	Producer                             *pb.ProducerManifest
	OntologyHash                         *pb.ContentHash
	MaximumReferences, MaximumCandidates int
}

// PreparedGraphAssembly is an artifact locator, never a scheduling capability.
type PreparedGraphAssembly struct {
	Plan      *pb.GraphAssemblyPlan
	Reference *pb.ArtifactRef
}

// ErrGraphAssemblyUnresolved keeps unresolved sources out of worker dispatch;
// the current Rust builder requires one LINK/CREATE assignment per mention.
var ErrGraphAssemblyUnresolved = errors.New("ASSEMBLE requires resolved LINK/CREATE assignments; source needs resolution")

func PrepareGraphAssembly(ctx context.Context, store GraphAssemblyPreparationStore, reader DocumentArtifactReader,
	writer GraphSourceArtifactWriter, pin domain.SnapshotPin, config GraphAssemblyPreparationConfig) (*PreparedGraphAssembly, error) {
	if ctx == nil || store == nil || reader == nil || writer == nil || config.CorpusID == "" || config.PublicationID == "" || config.SourceJobID == "" ||
		config.MaximumReferences <= 0 || config.MaximumReferences > domain.DefaultWireLimits.MaxItems || config.MaximumCandidates <= 0 || config.MaximumCandidates > domain.DefaultWireLimits.MaxItems {
		return nil, errors.New("bounded graph preparation dependencies required")
	}
	for _, m := range []proto.Message{config.Producer, config.OntologyHash} {
		if err := domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	binding, err := store.LoadGraphSourceBinding(bounded, config.CorpusID, config.PublicationID, config.SourceJobID)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateGraphSourceBinding(binding); err != nil {
		return nil, err
	}
	if binding.Source.Snapshot.CorpusId != config.CorpusID || binding.PublicationID != config.PublicationID || binding.Source.SourceJobID != config.SourceJobID {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = store.VerifyPublishedGraphSourceBinding(bounded, pin, binding.Source); err != nil {
		return nil, err
	}
	original, err := ReadGraphResolutionReceipt(bounded, store, reader, config.CorpusID, config.SourceJobID,
		binding.SourceCheckpointID, binding.OriginalExtraction, binding.OriginalResolution, config.MaximumReferences, config.MaximumCandidates)
	if err != nil {
		return nil, err
	}
	if original.RegistryRevision != binding.RegistryRevision {
		return nil, domain.ErrResolutionReplan
	}

	remaining := uint64(domain.DefaultWireLimits.MaxBytes)
	document, extraction, resolution := new(pb.DocumentBatch), new(pb.ExtractionBatch), new(pb.ResolutionBatch)
	var documentBytes []byte
	refs := []*pb.ArtifactRef{binding.Source.Bound, binding.BoundExtraction, binding.BoundResolution}
	for i, message := range []proto.Message{document, extraction, resolution} {
		raw, e := readGraphPreparationArtifact(bounded, store, reader, config.CorpusID, refs[i], &remaining)
		if e != nil {
			return nil, e
		}
		if e = domain.DecodeWire(raw, message, domain.DefaultWireLimits); e != nil {
			return nil, e
		}
		if i == 0 {
			documentBytes = raw
		}
	}
	if err = store.VerifyDocumentRegistryView(bounded, config.CorpusID, binding.Source.Bound, documentBytes, binding.RegistryRevision, config.MaximumReferences); err != nil {
		return nil, err
	}
	if !proto.Equal(extraction.SourceDocumentBatch, binding.Source.Bound) || !proto.Equal(document.Context.SnapshotRef, binding.Source.Snapshot) ||
		resolution.RegistryRevision != binding.RegistryRevision {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateExtractionBatchClosure(extraction, document, config.MaximumReferences); err != nil {
		return nil, err
	}
	if err = domain.ValidateGraphResolutionSource(extraction, binding.BoundExtraction, resolution, config.MaximumReferences); err != nil {
		return nil, err
	}
	if !containsExpectedHash(extraction.Dependencies.ProducerManifest.InputHashes, config.OntologyHash) ||
		!proto.Equal(extraction.Context.ConfigFingerprint, document.Context.ConfigFingerprint) {
		return nil, errors.New("ASSEMBLE ontology/config differs from extraction producer")
	}
	selection, err := graphAssemblyCanonicalSelection(resolution)
	if err != nil {
		return nil, err
	}
	plan := &pb.GraphAssemblyPlan{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: config.CorpusID},
		Context: proto.Clone(document.Context).(*pb.RequestContext), PublicationId: binding.PublicationID, PublicationFence: binding.Fence,
		TargetSequence: binding.TargetSequence, RegistryRevision: binding.RegistryRevision, DocumentBatch: proto.Clone(binding.Source.Bound).(*pb.ArtifactRef),
		ExtractionBatch: proto.Clone(binding.BoundExtraction).(*pb.ArtifactRef), ResolutionBatch: proto.Clone(binding.BoundResolution).(*pb.ArtifactRef),
		SourceCheckpointId: binding.SourceCheckpointID, ProducerManifest: proto.Clone(config.Producer).(*pb.ProducerManifest), OntologyHash: proto.Clone(config.OntologyHash).(*pb.ContentHash)}
	// Deterministic protobuf seed binds target, checkpoint, exact source refs,
	// producer and ontology without creating a circular hash reference.
	seed, err := proto.MarshalOptions{Deterministic: true}.Marshal(plan)
	if err != nil {
		return nil, err
	}
	id := fmt.Sprintf("%x", sha256.Sum256(seed))
	plan.Meta.RecordId, plan.OutputArtifactId = "plan:assembly:"+id, "delta:assembly:"+id
	plan.Context.RequestId, plan.Context.TraceId = plan.Meta.RecordId, plan.Meta.RecordId
	view, err := store.ExportRegistryEntityView(bounded, domain.RegistryEntityExport{ViewID: "view:assembly:" + id,
		CorpusID: config.CorpusID, PublicationID: binding.PublicationID, Fence: binding.Fence, Revision: binding.RegistryRevision,
		EntityIDs: selection, Producer: config.Producer, MaximumEntities: config.MaximumReferences, MaximumBytes: int(remaining)})
	if err != nil {
		return nil, err
	}
	viewArtifact, err := graphPreparationArtifact(view, view.GetMeta().GetRecordId(), domain.RegistryEntityViewMediaType)
	if err != nil {
		return nil, err
	}
	plan.RegistryView = viewArtifact.Reference
	if err = domain.ValidateAssemblyRegistryBinding(plan, view); err != nil {
		return nil, err
	}
	if len(view.RequestedIds) != len(selection) {
		return nil, domain.ErrPersistentIntegrity
	}
	for i := range selection {
		if selection[i] != view.RequestedIds[i] {
			return nil, domain.ErrPersistentIntegrity
		}
	}
	planArtifact, err := graphPreparationArtifact(plan, plan.Meta.RecordId, domain.GraphAssemblyPlanMediaType)
	if err != nil {
		return nil, err
	}
	for _, artifact := range []domain.GraphSourceArtifact{viewArtifact, planArtifact} {
		if artifact.Reference.ByteSize > remaining {
			return nil, errors.New("ASSEMBLE plan/view exceed worker byte budget")
		}
		remaining -= artifact.Reference.ByteSize
	}
	if err = budgetGraphAssemblyTexts(document, extraction, &remaining); err != nil {
		return nil, err
	}
	if err = store.VerifyGraphAssemblyPublication(bounded, plan); err != nil {
		return nil, err
	}
	for _, artifact := range []domain.GraphSourceArtifact{viewArtifact, planArtifact} {
		if _, err = writer.Put(bounded, artifact.Reference, bytes.NewReader(artifact.Bytes)); err != nil {
			return nil, err
		}
		if err = store.RegisterArtifact(bounded, config.CorpusID, artifact.Reference); err != nil {
			return nil, err
		}
		dependencies := []*pb.Dependency{{DependencyId: "ontology:assembly", Fingerprint: proto.Clone(config.OntologyHash).(*pb.ContentHash)}}
		for _, ref := range refs {
			dependencies = append(dependencies, &pb.Dependency{DependencyId: ref.ArtifactId, Fingerprint: proto.Clone(ref.ContentHash).(*pb.ContentHash)})
		}
		if artifact.Reference.ArtifactId == planArtifact.Reference.ArtifactId {
			dependencies = append(dependencies, &pb.Dependency{DependencyId: viewArtifact.Reference.ArtifactId, Fingerprint: proto.Clone(viewArtifact.Reference.ContentHash).(*pb.ContentHash)})
		}
		manifest := &pb.DependencyManifest{ArtifactId: artifact.Reference.ArtifactId, ProducerManifest: proto.Clone(config.Producer).(*pb.ProducerManifest), Dependencies: dependencies}
		if err = store.EnsureArtifactDependencyManifest(bounded, config.CorpusID, artifact.Reference.ArtifactId, manifest); err != nil {
			return nil, err
		}
	}
	if err = store.VerifyGraphAssemblySourceCheckpoint(bounded, config.CorpusID, config.SourceJobID, binding.SourceCheckpointID, binding.OriginalResolution); err != nil {
		return nil, err
	}
	if err = store.VerifyPublishedGraphSourceBinding(bounded, pin, binding.Source); err != nil {
		return nil, err
	}
	if err = store.VerifyGraphAssemblyPublication(bounded, plan); err != nil {
		return nil, err
	}
	return &PreparedGraphAssembly{Plan: plan, Reference: planArtifact.Reference}, nil
}

func graphPreparationArtifact(message proto.Message, id, media string) (domain.GraphSourceArtifact, error) {
	if err := domain.ValidateWire(message, domain.DefaultWireLimits); err != nil {
		return domain.GraphSourceArtifact{}, err
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return domain.GraphSourceArtifact{}, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	ref := &pb.ArtifactRef{ArtifactId: id, ContentHash: &pb.ContentHash{Sha256: hash}, ByteSize: uint64(len(raw)), SchemaVersion: 1,
		MediaType: media, StorageKey: "sha256/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".bin"}
	if err = domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return domain.GraphSourceArtifact{}, err
	}
	return domain.GraphSourceArtifact{Reference: ref, Bytes: raw}, nil
}

func budgetGraphAssemblyTexts(document *pb.DocumentBatch, extraction *pb.ExtractionBatch, remaining *uint64) error {
	needed := map[string]bool{}
	for _, mention := range extraction.Mentions {
		needed[mention.TextSpan.TextArtifactId] = true
	}
	for _, support := range extraction.Supports {
		for _, span := range support.EvidenceSpans {
			needed[span.TextArtifactId] = true
		}
	}
	seen := map[string]bool{}
	for _, text := range document.TextArtifacts {
		id := text.Meta.RecordId
		if seen[id] {
			return errors.New("duplicate ASSEMBLE text descriptor")
		}
		seen[id] = true
		if !needed[id] {
			continue
		}
		ref := text.NormalizedTextRef
		if ref == nil || ref.MediaType != "text/plain;charset=utf-8" || ref.ByteSize > *remaining {
			return errors.New("ASSEMBLE text type or byte budget mismatch")
		}
		*remaining -= ref.ByteSize
		delete(needed, id)
	}
	if len(needed) != 0 {
		return errors.New("ASSEMBLE normalized text descriptor missing")
	}
	return nil
}

// Caller has validated wire bounds and full proposal/decision/mention closure.
// Match Rust materialize_resolved_relations; never silently omit DEFER records.
func graphAssemblyCanonicalSelection(resolution *pb.ResolutionBatch) ([]string, error) {
	ids := map[string]bool{}
	for _, decision := range resolution.Decisions {
		if (decision.Action != pb.ResolutionAction_RESOLUTION_ACTION_LINK && decision.Action != pb.ResolutionAction_RESOLUTION_ACTION_CREATE) || len(decision.AssignedCanonicalIds) != 1 {
			return nil, ErrGraphAssemblyUnresolved
		}
		ids[decision.AssignedCanonicalIds[0]] = true
	}
	selection := make([]string, 0, len(ids))
	for id := range ids {
		selection = append(selection, id)
	}
	sort.Strings(selection)
	return selection, nil
}
