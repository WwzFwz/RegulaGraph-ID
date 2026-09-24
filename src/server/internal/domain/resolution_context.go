// Validates the two sides of contextual identity resolution without performing model inference.
// Workflows authenticate alias/support ownership and immutable artifact bytes; this domain gate
// checks corpus/type/source/span correlation and requires LINK to cite both mention and candidate
// context. Excerpts remain untrusted data and never constitute review approval. Linear indexed
// validation has an explicit work cap; measure validation time, candidate coverage and false links
// under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED), independently of model quality.
package domain

import (
	"errors"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type resolutionContextIndex struct {
	items      map[string]*pb.TextItem
	mention    map[string]bool
	candidates map[string]map[string]bool
	remaining  int
}

// ValidateSemanticResolutionItem assumes the outer wire decoder has bounded bytes/depth.
func ValidateSemanticResolutionItem(item *pb.AmbiguousMention, corpus string, maximumWork int) error {
	_, err := indexResolutionContext(item, corpus, maximumWork)
	return err
}

func indexResolutionContext(item *pb.AmbiguousMention, corpus string, maximumWork int) (*resolutionContextIndex, error) {
	if item == nil || item.Mention == nil || item.Mention.GetMeta().GetCorpusId() != corpus ||
		item.ExpectedRegistryRevision == 0 || maximumWork <= 0 {
		return nil, errors.New("resolution requires mention corpus, revision and work budget")
	}
	expected := &pb.Provenance{Sources: item.Mention.SourceRefs, Spans: []*pb.TextSpan{item.Mention.TextSpan}}
	if !proto.Equal(item.Evidence, expected) {
		return nil, errors.New("resolution evidence differs from exact mention provenance")
	}
	index := &resolutionContextIndex{items: map[string]*pb.TextItem{}, candidates: map[string]map[string]bool{}, remaining: maximumWork}
	entities := map[string]*pb.CanonicalEntity{}
	for _, entity := range item.Candidates {
		if err := index.consume(); err != nil {
			return nil, err
		}
		id := entity.GetMeta().GetRecordId()
		if id == "" || entity.GetMeta().GetCorpusId() != corpus || entities[id] != nil ||
			entity.EntityType != item.Mention.CandidateType || entity.RegistryRevision == 0 ||
			entity.RegistryRevision > item.ExpectedRegistryRevision {
			return nil, errors.New("candidate identity, corpus, type or revision differs")
		}
		entities[id] = entity
	}
	var err error
	index.mention, err = index.add(item.Mention, item.ContextItems)
	if err != nil {
		return nil, err
	}
	type supportKey struct{ canonical, alias, mention string }
	seen := map[supportKey]bool{}
	for _, evidence := range item.CandidateContexts {
		if err = index.consume(); err != nil {
			return nil, err
		}
		if evidence == nil || entities[evidence.CanonicalId] == nil || evidence.AliasId == "" ||
			evidence.SupportMention.GetMeta().GetCorpusId() != corpus ||
			evidence.SupportMention.GetCandidateType() != item.Mention.CandidateType {
			return nil, errors.New("candidate context lacks matching owner, alias or source mention")
		}
		key := supportKey{evidence.CanonicalId, evidence.AliasId, evidence.SupportMention.Meta.RecordId}
		if seen[key] {
			return nil, errors.New("candidate context repeats alias support")
		}
		seen[key] = true
		covered, err := index.add(evidence.SupportMention, evidence.ContextItems)
		if err != nil {
			return nil, err
		}
		if index.candidates[evidence.CanonicalId] == nil {
			index.candidates[evidence.CanonicalId] = map[string]bool{}
		}
		for id := range covered {
			index.candidates[evidence.CanonicalId][id] = true
		}
	}
	return index, nil
}

func (index *resolutionContextIndex) consume() error {
	if index.remaining <= 0 {
		return errors.New("resolution context work budget exceeded")
	}
	index.remaining--
	return nil
}

func (index *resolutionContextIndex) add(mention *pb.Mention, excerpts []*pb.TextItem) (map[string]bool, error) {
	if mention == nil || mention.TextSpan == nil || len(mention.SourceRefs) == 0 || len(excerpts) == 0 {
		return nil, errors.New("resolution requires hydrated source context")
	}
	sources := map[string]bool{}
	for _, source := range mention.SourceRefs {
		if err := index.consume(); err != nil {
			return nil, err
		}
		if source == nil || source.SourceBlobId == "" {
			return nil, errors.New("mention source is missing")
		}
		sources[source.SourceBlobId] = true
	}
	seen, covered := map[string]bool{}, map[string]bool{}
	for _, excerpt := range excerpts {
		if err := index.consume(); err != nil {
			return nil, err
		}
		if excerpt == nil || excerpt.ItemId == "" || seen[excerpt.ItemId] || excerpt.Text == "" ||
			!utf8.ValidString(excerpt.Text) || excerpt.Provenance == nil ||
			len(excerpt.Provenance.Spans) != 1 || len(excerpt.Provenance.Sources) == 0 {
			return nil, errors.New("context requires unique IDs, UTF-8 and source provenance")
		}
		seen[excerpt.ItemId] = true
		span := excerpt.Provenance.Spans[0]
		if span == nil || span.EndByte <= span.StartByte || span.EndByte-span.StartByte != uint64(len(excerpt.Text)) {
			return nil, errors.New("context span differs from excerpt bytes")
		}
		for _, source := range excerpt.Provenance.Sources {
			if err := index.consume(); err != nil {
				return nil, err
			}
			if source == nil || !sources[source.SourceBlobId] {
				return nil, errors.New("context source differs from its supporting mention")
			}
		}
		if existing := index.items[excerpt.ItemId]; existing != nil && !proto.Equal(existing, excerpt) {
			return nil, errors.New("context ID has conflicting text or provenance")
		}
		index.items[excerpt.ItemId] = excerpt
		target := mention.TextSpan
		if span.TextArtifactId == target.TextArtifactId && span.StartByte <= target.StartByte && span.EndByte >= target.EndByte {
			start, end := target.StartByte-span.StartByte, target.EndByte-span.StartByte
			if end <= start || !utf8.ValidString(excerpt.Text[start:end]) || excerpt.Text[start:end] != mention.SurfaceForm {
				return nil, errors.New("mention differs from context bytes")
			}
			covered[excerpt.ItemId] = true
		}
	}
	if len(covered) == 0 {
		return nil, errors.New("context does not cover its supporting mention")
	}
	return covered, nil
}

// ValidateResolutionContextReferences applies identically at the gateway and workflow return
// boundary. The model must not support LINK solely with a candidate label or another candidate.
func ValidateResolutionContextReferences(item *pb.AmbiguousMention, proposal *pb.ResolutionProposal, maximumWork int) error {
	if item == nil || proposal == nil || strings.TrimSpace(proposal.GetRationale()) == "" {
		return errors.New("resolution explanation is missing")
	}
	index, err := indexResolutionContext(item, item.Mention.GetMeta().GetCorpusId(), maximumWork)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	mentionCited, candidateCited := false, false
	for _, id := range proposal.SupportingContextIds {
		if err := index.consume(); err != nil {
			return err
		}
		if index.items[id] == nil || seen[id] {
			return errors.New("resolution cites unknown or repeated context")
		}
		seen[id] = true
		mentionCited = mentionCited || index.mention[id]
		if len(proposal.CandidateIds) == 1 {
			candidateCited = candidateCited || index.candidates[proposal.CandidateIds[0]][id]
		}
	}
	if !mentionCited {
		return errors.New("resolution must cite the source mention context")
	}
	if proposal.Action == pb.ResolutionAction_RESOLUTION_ACTION_LINK && (len(proposal.CandidateIds) != 1 || !candidateCited) {
		return errors.New("LINK must cite verified context for the selected candidate")
	}
	return nil
}
