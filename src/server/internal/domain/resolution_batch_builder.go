// Builds the immutable RESOLVE artifact only from a checked registry receipt and pinned inputs.
// Go owns this post-decision handoff: source and candidate artifact hashes, negative lookup
// revisions, model identity, and every registry assignment remain available to ASSEMBLE.
// The caller must authenticate the PostgreSQL transaction and artifact bytes before this pure
// function; measure allocation/p95/p99 and resolution quality under required benchmark targets.
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// AssembleResolutionBatchFromReceipt copies checked decisions into a complete, deterministic
// artifact. It never converts a registry error into a successful or partial publication.
func AssembleResolutionBatchFromReceipt(source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef,
	candidates *pb.RegistryCandidateBatch, candidateRef *pb.ArtifactRef,
	request *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse,
	model *pb.ModelManifest, producer *pb.ProducerManifest, usage *pb.TokenUsage,
	recordID string, maximumReferences, maximumCandidatesPerMention int) (*pb.ResolutionBatch, error) {
	if candidateRef == nil || candidateRef.ContentHash == nil || candidates == nil ||
		candidateRef.ArtifactId == "" || recordID == "" || model == nil || producer == nil ||
		usage == nil || model.Task != pb.ModelTask_MODEL_TASK_RESOLVE ||
		maximumReferences <= 0 || maximumCandidatesPerMention <= 0 ||
		proto.Size(candidateRef) > DefaultWireLimits.MaxBytes {
		return nil, errors.New("bounded pinned candidate, RESOLVE model, and output identity are required")
	}
	if err := ValidateRegistryResolveReceipt(source, sourceRef, candidates, request, response,
		maximumReferences, maximumCandidatesPerMention); err != nil {
		return nil, fmt.Errorf("registry receipt is not publishable: %w", err)
	}
	if candidateRef.ArtifactId == sourceRef.ArtifactId {
		return nil, errors.New("candidate artifact reuses extraction artifact ID")
	}
	if err := ValidateWire(candidateRef, DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("candidate artifact ref is invalid: %w", err)
	}
	for _, input := range []proto.Message{model, producer, usage} {
		if err := ValidateWire(input, DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("resolution producer input is invalid: %w", err)
		}
	}
	meta := &pb.RecordMeta{SchemaVersion: source.Meta.SchemaVersion,
		CorpusId: source.Meta.CorpusId, RecordId: recordID}
	if err := ValidateWire(meta, DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("resolution output identity is invalid: %w", err)
	}
	// Count all cloned inputs before copying any caller-owned message. This is deliberately
	// conservative: repeated fields are counted even when only a subset is emitted.
	remainingBytes := DefaultWireLimits.MaxBytes
	for _, input := range []proto.Message{meta, request.Context, sourceRef, candidateRef,
		request, response, model, producer, usage, candidates.Dependencies} {
		size := proto.Size(input)
		if size > remainingBytes {
			return nil, errors.New("resolution input exceeds aggregate output byte budget")
		}
		remainingBytes -= size
	}
	if recordID == source.Meta.RecordId || recordID == candidates.Meta.RecordId ||
		recordID == sourceRef.ArtifactId || recordID == candidateRef.ArtifactId {
		return nil, errors.New("resolution batch ID collides with input")
	}
	for _, candidate := range candidates.Candidates {
		if recordID == candidate.GetMeta().GetRecordId() {
			return nil, errors.New("resolution batch ID collides with candidate")
		}
	}
	for _, alias := range candidates.Aliases {
		if recordID == alias.GetMeta().GetRecordId() {
			return nil, errors.New("resolution batch ID collides with alias")
		}
	}
	proposals := make([]*pb.ResolutionProposal, 0, len(request.Proposals))
	decisions := make([]*pb.ResolutionDecision, 0, len(response.Assignments))
	for _, proposal := range request.Proposals {
		proposals = append(proposals, proto.Clone(proposal).(*pb.ResolutionProposal))
		if recordID == proposal.Meta.RecordId {
			return nil, errors.New("resolution batch ID collides with proposal")
		}
	}
	for _, assignment := range response.Assignments {
		decisions = append(decisions, proto.Clone(assignment.GetDecision()).(*pb.ResolutionDecision))
		if recordID == assignment.GetDecision().GetMeta().GetRecordId() {
			return nil, errors.New("resolution batch ID collides with decision")
		}
	}
	sort.Slice(proposals, func(i, j int) bool {
		return proposals[i].Meta.RecordId < proposals[j].Meta.RecordId
	})
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].ProposalId < decisions[j].ProposalId
	})
	revisions := make([]*pb.LookupScopeRevision, 0, len(candidates.Dependencies.LookupScopeRevisions))
	for _, revision := range candidates.Dependencies.LookupScopeRevisions {
		revisions = append(revisions, proto.Clone(revision).(*pb.LookupScopeRevision))
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].ScopeId < revisions[j].ScopeId })
	manifestID := sha256.Sum256([]byte(recordID))
	batch := &pb.ResolutionBatch{
		Meta:                  meta,
		Context:               proto.Clone(request.Context).(*pb.RequestContext),
		SourceExtractionBatch: proto.Clone(sourceRef).(*pb.ArtifactRef),
		Proposals:             proposals,
		Decisions:             decisions,
		Dependencies: &pb.DependencyManifest{
			ArtifactId: fmt.Sprintf("dependencies:%x", manifestID),
			Dependencies: []*pb.Dependency{
				{DependencyId: sourceRef.ArtifactId, Fingerprint: proto.Clone(sourceRef.ContentHash).(*pb.ContentHash)},
				{DependencyId: candidateRef.ArtifactId, Fingerprint: proto.Clone(candidateRef.ContentHash).(*pb.ContentHash)},
			},
			ProducerManifest:     proto.Clone(producer).(*pb.ProducerManifest),
			LookupScopeRevisions: revisions,
		},
		Completeness:     pb.Completeness_COMPLETENESS_COMPLETE,
		OntologyVersion:  source.OntologyVersion,
		RegistryRevision: response.RegistryRevision,
		ModelManifest:    proto.Clone(model).(*pb.ModelManifest),
		ItemCounts:       &pb.Counts{Expected: uint64(len(source.Mentions)), Accepted: uint64(len(source.Mentions))},
		TokenUsage:       proto.Clone(usage).(*pb.TokenUsage),
	}
	if err := ValidateResolutionBatchClosure(batch, source, sourceRef, maximumReferences); err != nil {
		return nil, fmt.Errorf("resolution batch closure: %w", err)
	}
	if err := ValidateWire(batch, DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("resolution batch wire: %w", err)
	}
	return batch, nil
}
