// Validates a worker's additive GraphDelta against authenticated ASSEMBLE inputs.
// This admission boundary reconstructs the defined canonical hash-v1 projection,
// not model extraction or registry decisions. Every output must match the source
// projection, including supports, negative dependencies and target visibility.
// Caller verifies artifact bytes, registry receipt and live publication authority.
// Work is bounded by wire/reference budgets; measure validation p95/RSS separately
// from Rust assembly under configs/benchmark-targets.yaml (not yet measured).
package domain

import (
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const GraphDeltaMediaType = "application/x-protobuf; message=regulagraph.v1.GraphDelta"

// GraphOutputSources holds decoded authenticated C01 inputs, not a parallel wire
// format. NormalizedTexts is keyed by TextArtifact ID, not by blob storage key.
type GraphOutputSources struct {
	Document        *pb.DocumentBatch
	Extraction      *pb.ExtractionBatch
	Resolution      *pb.ResolutionBatch
	Registry        *pb.RegistryEntityView
	NormalizedTexts map[string][]byte
}

func ValidatePlannedGraphDelta(delta *pb.GraphDelta, plan *pb.GraphAssemblyPlan, in GraphOutputSources, ontology *Ontology) error {
	if err := ValidateAssemblyRegistryBinding(plan, in.Registry); err != nil {
		return err
	}
	if len(in.NormalizedTexts) > DefaultWireLimits.MaxItems {
		return errors.New("graph text count exceeds validation budget")
	}
	remaining := 64 << 20
	for _, m := range []proto.Message{delta, plan, in.Document, in.Extraction, in.Resolution, in.Registry} {
		if err := ValidateWire(m, DefaultWireLimits); err != nil {
			return err
		}
		if !graphAssemblyKnownFields(m.ProtoReflect()) {
			return errors.New("unknown ASSEMBLE output/source fields")
		}
		size := proto.Size(m)
		if size > remaining {
			return errors.New("graph validation aggregate byte budget exceeded")
		}
		remaining -= size
	}
	for _, raw := range in.NormalizedTexts {
		if len(raw) > remaining {
			return errors.New("graph source text budget exceeded")
		}
		remaining -= len(raw)
	}
	d, e, r := in.Document, in.Extraction, in.Resolution
	workerRemaining := uint64(DefaultWireLimits.MaxBytes)
	for _, size := range []uint64{uint64(proto.Size(plan)), plan.DocumentBatch.ByteSize, plan.ExtractionBatch.ByteSize, plan.ResolutionBatch.ByteSize, plan.RegistryView.ByteSize} {
		if size > workerRemaining {
			return errors.New("graph inputs exceed worker byte budget")
		}
		workerRemaining -= size
	}
	if ontology == nil || !proto.Equal(ontology.ContentHash(), plan.OntologyHash) || ontology.Version() != e.OntologyVersion ||
		d.Meta.SchemaVersion != 1 || d.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || r.RegistryRevision != plan.RegistryRevision ||
		!proto.Equal(e.SourceDocumentBatch, plan.DocumentBatch) || !proto.Equal(r.SourceExtractionBatch, plan.ExtractionBatch) {
		return errors.New("graph source role, completeness, revision or ontology mismatch")
	}
	for _, c := range []*pb.RequestContext{d.Context, e.Context, r.Context} {
		if c.SchemaVersion != 1 || c.CorpusId != plan.Meta.CorpusId || c.AuthScopeRef != plan.Context.AuthScopeRef ||
			!proto.Equal(c.SnapshotRef, plan.Context.SnapshotRef) || !proto.Equal(c.ConfigFingerprint, plan.Context.ConfigFingerprint) {
			return errors.New("graph source context differs from plan")
		}
	}
	if err := ValidateDocumentBatchClosure(d, DefaultWireLimits.MaxItems); err != nil {
		return err
	}
	if err := ValidateExtractionBatchClosure(e, d, DefaultWireLimits.MaxItems); err != nil {
		return err
	}
	if err := ValidateGraphResolutionSource(e, plan.ExtractionBatch, r, DefaultWireLimits.MaxItems); err != nil {
		return err
	}
	if err := ontology.ValidateExtractionOntology(e); err != nil {
		return err
	}
	if err := BudgetGraphAssemblyTexts(d, e, &workerRemaining); err != nil {
		return err
	}
	if err := validateGraphOutputTexts(in); err != nil {
		return err
	}
	selection, err := GraphAssemblyCanonicalSelection(r)
	if err != nil {
		return err
	}
	if len(selection) != len(in.Registry.RequestedIds) {
		return errors.New("graph registry selection coverage mismatch")
	}
	for i, id := range selection {
		if in.Registry.RequestedIds[i] != id {
			return errors.New("graph registry selection differs from decisions")
		}
	}
	entities := map[string]*pb.CanonicalEntity{}
	for _, entity := range in.Registry.Entities {
		entities[entity.Meta.RecordId] = entity
	}
	proposals := map[string]*pb.ResolutionProposal{}
	for _, proposal := range r.Proposals {
		proposals[proposal.Meta.RecordId] = proposal
	}
	assigned := map[string]string{}
	for _, decision := range r.Decisions {
		for _, id := range proposals[decision.ProposalId].MentionIds {
			assigned[id] = decision.AssignedCanonicalIds[0]
		}
	}
	for _, mention := range e.Mentions {
		entity := entities[assigned[mention.Meta.RecordId]]
		if entity == nil || entity.EntityType != mention.CandidateType {
			return errors.New("graph assignment has missing or mismatched canonical type")
		}
	}
	assertions, supports, err := expectedGraphRelations(e, assigned, ontology)
	if err != nil {
		return err
	}
	dependencies, err := expectedGraphDependencies(plan, e, r, ontology)
	if err != nil {
		return err
	}
	expected := &pb.GraphDelta{
		Meta:         &pb.RecordMeta{SchemaVersion: 1, CorpusId: plan.Meta.CorpusId, RecordId: plan.OutputArtifactId},
		BaseSnapshot: proto.Clone(plan.Context.SnapshotRef).(*pb.SnapshotRef), RegistryRevision: plan.RegistryRevision,
		OntologyVersion: ontology.Version(), Assertions: assertions, Supports: supports, Dependencies: dependencies,
	}
	for _, v := range in.Registry.Entities {
		expected.Entities = append(expected.Entities, proto.Clone(v).(*pb.CanonicalEntity))
	}
	for _, v := range e.Mentions {
		expected.Mentions = append(expected.Mentions, proto.Clone(v).(*pb.Mention))
	}
	for _, v := range r.Decisions {
		expected.Decisions = append(expected.Decisions, proto.Clone(v).(*pb.ResolutionDecision))
	}
	sort.Slice(expected.Mentions, func(i, j int) bool { return expected.Mentions[i].Meta.RecordId < expected.Mentions[j].Meta.RecordId })
	sort.Slice(expected.Decisions, func(i, j int) bool { return expected.Decisions[i].Meta.RecordId < expected.Decisions[j].Meta.RecordId })
	seen := map[string]bool{plan.OutputArtifactId: true}
	var metas []*pb.RecordMeta
	for _, v := range expected.Entities {
		metas = append(metas, v.Meta)
	}
	for _, v := range expected.Mentions {
		metas = append(metas, v.Meta)
	}
	for _, v := range expected.Assertions {
		metas = append(metas, v.Meta)
	}
	for _, v := range expected.Supports {
		metas = append(metas, v.Meta)
	}
	for _, v := range expected.Decisions {
		metas = append(metas, v.Meta)
	}
	for _, meta := range metas {
		if meta.SchemaVersion != 1 || meta.CorpusId != plan.Meta.CorpusId || meta.Visibility != nil || seen[meta.RecordId] {
			return errors.New("graph source identity collision or premature visibility")
		}
		seen[meta.RecordId] = true
		meta.Visibility = &pb.Visibility{FromSeq: plan.TargetSequence}
	}
	expected.ValidationReport = &pb.ValidationReport{CheckedRecords: uint64(len(seen)), Valid: true}
	if !proto.Equal(delta, expected) {
		return fmt.Errorf("GraphDelta differs from complete source projection: %w", ErrPersistentIntegrity)
	}
	return nil
}
