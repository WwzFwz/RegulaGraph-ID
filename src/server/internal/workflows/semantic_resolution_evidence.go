// Hydrates candidate-side document evidence from the successful EXTRACT source catalog.
// Alias support IDs must resolve to exact mentions in hash-verified batches with matching
// corpus/snapshot/auth scope and document closure. All supporting structural chunks are retained;
// missing evidence or exhausted budgets fails explicitly before model sampling. Reads/text are
// reused within the batch and candidate copies reserve bytes before allocation. Input artifacts
// become dependencies of model audit/replay. Measure hydration p95/p99, RSS, support coverage and
// model identity accuracy separately; required benchmark targets remain REQUIRED_UNMEASURED.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type resolutionEvidenceSourceStore interface {
	LoadResolutionEvidenceSources(context.Context, *pb.RequestContext, []string, int) ([]*pb.ArtifactRef, error)
}

type resolutionHydrationBudget struct {
	maximum, used uint64
	work          int
	texts         map[string][]byte
	textRefs      map[string]*pb.ArtifactRef
}

func newResolutionHydrationBudget(maximum uint64, work int) *resolutionHydrationBudget {
	return &resolutionHydrationBudget{maximum: maximum, work: work, texts: map[string][]byte{}, textRefs: map[string]*pb.ArtifactRef{}}
}
func (b *resolutionHydrationBudget) reserve(size uint64) error {
	if size > b.maximum-b.used {
		return errors.New("resolution context/candidate expansion exceeds request byte budget")
	}
	b.used += size
	return nil
}

func uniqueModelDependencies(refs []*pb.ArtifactRef) ([]*pb.ArtifactRef, error) {
	seen := map[string]*pb.ArtifactRef{}
	result := make([]*pb.ArtifactRef, 0, len(refs))
	for _, ref := range refs {
		if ref == nil {
			return nil, domain.ErrPersistentIntegrity
		}
		if previous := seen[ref.ArtifactId]; previous != nil {
			if !proto.Equal(previous, ref) {
				return nil, domain.ErrPersistentIntegrity
			}
			continue
		}
		seen[ref.ArtifactId] = ref
		result = append(result, ref)
	}
	return result, nil
}

func (h *SemanticResolutionHandoff) hydrateCandidateEvidence(ctx context.Context, request *pb.SemanticResolveRequest,
	candidates *pb.RegistryCandidateBatch, artifacts SemanticResolutionArtifactReader,
	remaining *uint64, budget *resolutionHydrationBudget) ([]*pb.ArtifactRef, error) {
	if len(candidates.Candidates) == 0 {
		return nil, nil
	}
	store, ok := h.store.(resolutionEvidenceSourceStore)
	if !ok {
		return nil, errors.New("candidate resolution requires the verified extraction evidence catalog")
	}
	wanted := map[string]bool{}
	for _, alias := range candidates.Aliases {
		for _, id := range alias.SupportRefs {
			wanted[id] = true
		}
	}
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 || len(ids) > h.maximumReferences {
		return nil, errors.New("candidate evidence support budget is invalid")
	}
	refs, err := store.LoadResolutionEvidenceSources(ctx, request.Batch.Context, ids, h.maximumReferences)
	if err != nil {
		return nil, fmt.Errorf("lookup candidate source evidence: %w", err)
	}
	if len(refs) == 0 || len(refs) > h.maximumReferences {
		return nil, errors.New("candidate source count is invalid")
	}
	refs = append([]*pb.ArtifactRef(nil), refs...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].GetArtifactId() < refs[j].GetArtifactId() })
	dependencies := map[string]*pb.ArtifactRef{}
	read := func(ref *pb.ArtifactRef, message proto.Message) error {
		if ref == nil || ref.ByteSize == 0 || ref.ByteSize > *remaining || !resolutionProtoMedia(ref.MediaType, message) {
			return errors.New("candidate source artifact exceeds aggregate byte budget or media contract")
		}
		if previous := dependencies[ref.ArtifactId]; previous != nil && !proto.Equal(previous, ref) {
			return domain.ErrPersistentIntegrity
		}
		raw, err := artifacts.ReadVerified(ctx, ref, *remaining)
		if err != nil {
			return err
		}
		if uint64(len(raw)) != ref.ByteSize {
			return domain.ErrPersistentIntegrity
		}
		*remaining -= ref.ByteSize
		dependencies[ref.ArtifactId] = ref
		return domain.DecodeWire(raw, message, domain.DefaultWireLimits)
	}
	documents := map[string]*pb.DocumentBatch{}
	supports := map[string]*pb.AmbiguousMention{}
	for i, ref := range refs {
		if i != 0 && ref.GetArtifactId() == refs[i-1].GetArtifactId() {
			return nil, errors.New("candidate source lookup repeats an artifact")
		}
		source := new(pb.ExtractionBatch)
		if err = read(ref, source); err != nil {
			return nil, err
		}
		context := request.Batch.Context
		if source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || source.Meta.CorpusId != context.CorpusId ||
			source.Context.CorpusId != context.CorpusId || !proto.Equal(source.Context.SnapshotRef, context.SnapshotRef) ||
			source.Context.AuthScopeRef != context.AuthScopeRef || source.OntologyVersion != request.Batch.OntologyVersion {
			return nil, errors.New("candidate evidence source is incomplete or outside pinned corpus/snapshot/auth/ontology")
		}
		docRef := source.SourceDocumentBatch
		document := documents[docRef.ArtifactId]
		if document == nil {
			document = new(pb.DocumentBatch)
			if err = read(docRef, document); err != nil {
				return nil, err
			}
			if err = domain.ValidateDocumentBatchClosure(document, h.maximumReferences); err != nil {
				return nil, err
			}
			documents[docRef.ArtifactId] = document
		} else if !proto.Equal(dependencies[docRef.ArtifactId], docRef) {
			return nil, domain.ErrPersistentIntegrity
		}
		if err = domain.ValidateExtractionBatchClosure(source, document, h.maximumReferences); err != nil {
			return nil, err
		}
		// Keep only requested support mentions for hydration, but validate the whole artifact
		// first. An unexpected/unrelated catalog hit is rejected instead of used as evidence.
		selected := &pb.ExtractionBatch{}
		for _, mention := range source.Mentions {
			if wanted[mention.Meta.RecordId] {
				selected.Mentions = append(selected.Mentions, mention)
			}
		}
		if len(selected.Mentions) == 0 {
			return nil, errors.New("catalog artifact contains none of the candidate supports")
		}
		pending, err := hydrateResolutionRequestWithBudget(ctx, request.Batch, selected,
			&pb.RegistryCandidateBatch{RegistryRevision: candidates.RegistryRevision}, document, artifacts, remaining, budget)
		if err != nil {
			return nil, err
		}
		for _, item := range pending.Items {
			id := item.Mention.Meta.RecordId
			if previous := supports[id]; previous != nil && !proto.Equal(previous, item) {
				return nil, errors.New("candidate support ID has conflicting source bytes or context")
			}
			supports[id] = item
		}
	}
	for id := range wanted {
		if supports[id] == nil {
			return nil, errors.New("candidate alias support is absent from verified extraction")
		}
	}
	byCanonical := map[string][]*pb.ResolutionCandidateContext{}
	aliases := append([]*pb.Alias(nil), candidates.Aliases...)
	sort.Slice(aliases, func(i, j int) bool { return aliases[i].Meta.RecordId < aliases[j].Meta.RecordId })
	for _, alias := range aliases {
		supportIDs := append([]string(nil), alias.SupportRefs...)
		sort.Strings(supportIDs)
		for _, id := range supportIDs {
			item := supports[id]
			if item.Mention.SurfaceForm != alias.Surface {
				return nil, errors.New("alias surface differs from its source mention")
			}
			byCanonical[alias.CanonicalId] = append(byCanonical[alias.CanonicalId], &pb.ResolutionCandidateContext{
				CanonicalId: alias.CanonicalId, AliasId: alias.Meta.RecordId, SupportMention: item.Mention, ContextItems: item.ContextItems})
		}
	}
	for _, item := range request.Items {
		for _, entity := range item.Candidates {
			for _, evidence := range byCanonical[entity.Meta.RecordId] {
				if err = budget.reserve(uint64(proto.Size(evidence)) + 16); err != nil {
					return nil, err
				}
				item.CandidateContexts = append(item.CandidateContexts, proto.Clone(evidence).(*pb.ResolutionCandidateContext))
			}
		}
	}
	for id, ref := range budget.textRefs {
		if previous := dependencies[id]; previous != nil && !proto.Equal(previous, ref) {
			return nil, domain.ErrPersistentIntegrity
		}
		dependencies[id] = ref
	}
	ordered := make([]string, 0, len(dependencies))
	for id := range dependencies {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	output := make([]*pb.ArtifactRef, 0, len(ordered))
	for _, id := range ordered {
		output = append(output, dependencies[id])
	}
	return output, nil
}
