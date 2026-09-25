// Package domain projects authoritative legal-version filter facts from a
// verified DocumentBatch once, then checks each X01 IndexRecord against its
// source chunk, version, regulation and text blob. The view copies the facts
// needed by the writer so later mutation of caller-owned protobufs cannot
// change validation. No backend I/O occurs here. Owner page checks are cached
// per owner/text pair; owner and page intervals are sorted/merged once and
// overlap is looked up by binary search. Profile build RSS/p95 and per-record p95 under
// configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package domain

import (
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type indexVersionFacts struct {
	regulationID      string
	sourceBlobID      string
	jurisdiction      string
	interval          *pb.LegalInterval
	status            pb.LegalStatus
	indexable         bool
	visibility        *pb.Visibility
	blobVisible       *pb.Visibility
	textVisible       *pb.Visibility
	provisionVisible  *pb.Visibility
	regulationVisible *pb.Visibility
}

// IndexSourceView is immutable after construction. The original DocumentBatch
// still needs a verified artifact hash and snapshot membership from the caller.
type IndexSourceView struct {
	corpusID        string
	chunks          map[string]map[string]bool
	chunkVisibility map[string]*pb.Visibility
	versions        map[string]indexVersionFacts
}

// NewIndexSourceView validates the source once instead of rechecking a large
// DocumentBatch for every vector record in the batch. The caller still proves
// snapshot membership of every chunk/version/source at the target sequence;
// absent record visibility is not evidence of membership.
func NewIndexSourceView(batch *pb.DocumentBatch, maximumEdges int) (*IndexSourceView, error) {
	if batch == nil || maximumEdges <= 0 {
		return nil, errors.New("document batch and positive edge bound are required")
	}
	if err := ValidateWire(batch, DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("invalid source document batch: %w", err)
	}
	if batch.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return nil, errors.New("partial source document batch cannot publish an index")
	}
	if err := ValidateDocumentBatchClosure(batch, maximumEdges); err != nil {
		return nil, fmt.Errorf("source document closure: %w", err)
	}
	regulations := make(map[string]*pb.Regulation, len(batch.Regulations))
	sources := make(map[string]*pb.SourceBlob, len(batch.Sources))
	provisions := make(map[string]*pb.Provision, len(batch.Provisions))
	texts := make(map[string]*pb.TextArtifact, len(batch.TextArtifacts))
	structures := make(map[string]*pb.StructureNode, len(batch.Structures))
	for _, regulation := range batch.Regulations {
		regulations[regulation.Meta.RecordId] = regulation
	}
	for _, source := range batch.Sources {
		sources[source.Meta.RecordId] = source
	}
	for _, provision := range batch.Provisions {
		provisions[provision.Meta.RecordId] = provision
	}
	for _, text := range batch.TextArtifacts {
		texts[text.Meta.RecordId] = text
	}
	for _, structure := range batch.Structures {
		structures[structure.Meta.RecordId] = structure
	}
	view := &IndexSourceView{
		corpusID:        batch.Meta.CorpusId,
		chunks:          make(map[string]map[string]bool, len(batch.Chunks)),
		chunkVisibility: make(map[string]*pb.Visibility, len(batch.Chunks)),
		versions:        make(map[string]indexVersionFacts, len(batch.Versions)),
	}
	for _, version := range batch.Versions {
		provision := provisions[version.ProvisionId]
		if provision == nil || len(version.Spans) == 0 {
			return nil, errors.New("index version lacks local provision or text span")
		}
		regulation := regulations[provision.RegulationId]
		text := texts[version.Spans[0].TextArtifactId]
		if regulation == nil || text == nil || sources[text.SourceBlobId] == nil || version.LegalInterval == nil {
			return nil, errors.New("index version lacks legal/source facts")
		}
		view.versions[version.Meta.RecordId] = indexVersionFacts{
			regulationID:      provision.RegulationId,
			sourceBlobID:      text.SourceBlobId,
			jurisdiction:      regulation.Jurisdiction,
			interval:          proto.Clone(version.LegalInterval).(*pb.LegalInterval),
			status:            version.LegalStatus,
			visibility:        cloneVisibility(version.Meta.Visibility),
			blobVisible:       cloneVisibility(sources[text.SourceBlobId].Meta.Visibility),
			textVisible:       cloneVisibility(text.Meta.Visibility),
			provisionVisible:  cloneVisibility(provision.Meta.Visibility),
			regulationVisible: cloneVisibility(regulation.Meta.Visibility),
			indexable: version.ReviewState != pb.ReviewState_REVIEW_STATE_REJECTED &&
				version.ReviewState != pb.ReviewState_REVIEW_STATE_QUARANTINED,
		}
	}
	validatedOwnerPages := make(map[[2]string]bool, len(batch.Structures))
	type textPageKey struct {
		textID string
		page   uint32
	}
	pageRangesByKey := make(map[textPageKey][]byteRange)
	for _, chunk := range batch.Chunks {
		if chunk.TextSpan == nil || len(chunk.StructureNodeRefs) != 1 {
			return nil, errors.New("index chunk needs one primary span and owning structure")
		}
		text := texts[chunk.TextSpan.TextArtifactId]
		owner := structures[chunk.StructureNodeRefs[0]]
		if text == nil || owner == nil {
			return nil, errors.New("index chunk source or owning structure is unavailable")
		}
		ownerKey := [2]string{owner.Meta.RecordId, text.Meta.RecordId}
		if !validatedOwnerPages[ownerKey] {
			ownerRanges, err := indexMergedTextRanges(owner.SourceSpans, text.Meta.RecordId)
			if err != nil {
				return nil, err
			}
			validatedPages := make(map[uint32]bool, len(owner.PageLocators))
			for _, locator := range owner.PageLocators {
				if locator == nil {
					return nil, errors.New("index chunk owning structure has a nil page locator")
				}
				page := int(locator.PageNumber) - 1
				if locator.SourceBlobId != text.SourceBlobId || page < 0 || page >= len(text.PageResults) ||
					text.PageResults[page].PageNumber != locator.PageNumber ||
					text.PageResults[page].Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
					return nil, errors.New("index chunk owning structure has an invalid page locator")
				}
				if !validatedPages[locator.PageNumber] {
					pageKey := textPageKey{textID: text.Meta.RecordId, page: locator.PageNumber}
					pageRanges, found := pageRangesByKey[pageKey]
					if !found {
						pageRanges, err = indexMergedTextRanges(text.PageResults[page].Spans, text.Meta.RecordId)
						if err != nil {
							return nil, fmt.Errorf("index page %d: %w", locator.PageNumber, err)
						}
						pageRangesByKey[pageKey] = pageRanges
					}
					if !indexRangesOverlap(ownerRanges, pageRanges) {
						return nil, errors.New("index chunk owning structure locator does not overlap its page")
					}
					validatedPages[locator.PageNumber] = true
				}
			}
			validatedOwnerPages[ownerKey] = true
		}
		versions := make(map[string]bool, len(chunk.ProvisionVersionRefs))
		for _, id := range chunk.ProvisionVersionRefs {
			versions[id] = true
		}
		view.chunks[chunk.Meta.RecordId] = versions
		view.chunkVisibility[chunk.Meta.RecordId] = cloneVisibility(chunk.Meta.Visibility)
	}
	return view, nil
}

// indexMergedTextRanges sorts and merges actual source/page intervals once.
// Empty, foreign and nil spans fail closed before logarithmic overlap lookups.
func indexMergedTextRanges(spans []*pb.TextSpan, textID string) ([]byteRange, error) {
	ranges := make([]byteRange, 0, len(spans))
	for _, span := range spans {
		if span == nil || span.TextArtifactId != textID || span.StartByte >= span.EndByte {
			return nil, errors.New("index source has foreign or empty spans")
		}
		ranges = append(ranges, byteRange{start: span.StartByte, end: span.EndByte})
	}
	if len(ranges) == 0 {
		return nil, errors.New("index source has no text span")
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	merged := ranges[:1]
	for _, next := range ranges[1:] {
		last := &merged[len(merged)-1]
		if next.start <= last.end {
			if next.end > last.end {
				last.end = next.end
			}
		} else {
			merged = append(merged, next)
		}
	}
	return merged, nil
}

// Both slices are sorted, merged, and have increasing end offsets. Iterating
// the shorter one bounds the repeated work for either long owners or pages.
func indexRangesOverlap(left, right []byteRange) bool {
	if len(left) > len(right) {
		left, right = right, left
	}
	for _, candidate := range left {
		position := sort.Search(len(right), func(i int) bool { return right[i].end > candidate.start })
		if position < len(right) && right[position].start < candidate.end {
			return true
		}
	}
	return false
}

// ValidateRecord ensures legal intervals/statuses and source blobs in a new
// IndexRecord are exact projections of the verified chunk's provision versions.
func (view *IndexSourceView) ValidateRecord(record *pb.IndexRecord) error {
	if view == nil {
		return errors.New("verified index source view is required")
	}
	if err := ValidatePairedIndexFilters(record); err != nil {
		return err
	}
	if record.Meta.CorpusId != view.corpusID {
		return errors.New("index record belongs to another corpus")
	}
	chunkVersions, found := view.chunks[record.ChunkId]
	if !found || len(chunkVersions) != len(record.ProvisionVersionRefs) {
		return errors.New("index chunk or version membership differs from source")
	}
	if !visibilityContains(view.chunkVisibility[record.ChunkId], record.Meta.Visibility) {
		return errors.New("index visibility exceeds source chunk visibility")
	}
	for _, id := range record.ProvisionVersionRefs {
		if !chunkVersions[id] {
			return errors.New("index version is absent from source chunk")
		}
	}
	for _, filter := range record.FilterMetadata.ProvisionFilters {
		facts, found := view.versions[filter.ProvisionVersionId]
		if !found || !facts.indexable ||
			!visibilityContains(facts.visibility, record.Meta.Visibility) ||
			!visibilityContains(facts.blobVisible, record.Meta.Visibility) ||
			!visibilityContains(facts.textVisible, record.Meta.Visibility) ||
			!visibilityContains(facts.provisionVisible, record.Meta.Visibility) ||
			!visibilityContains(facts.regulationVisible, record.Meta.Visibility) ||
			filter.RegulationId != facts.regulationID ||
			filter.SourceBlobId != facts.sourceBlobID ||
			filter.Jurisdiction != facts.jurisdiction ||
			filter.LegalStatus != facts.status ||
			!proto.Equal(filter.LegalInterval, facts.interval) {
			return errors.New("index legal/source filter differs from verified version")
		}
	}
	return nil
}

func cloneVisibility(value *pb.Visibility) *pb.Visibility {
	if value == nil {
		return nil
	}
	return proto.Clone(value).(*pb.Visibility)
}

func visibilityContains(source, indexed *pb.Visibility) bool {
	if source == nil {
		return true // membership for missing visibility remains a caller precondition
	}
	return indexed != nil && indexed.FromSeq >= source.FromSeq &&
		(source.ToSeq == nil || indexed.ToSeq != nil && *indexed.ToSeq <= *source.ToSeq)
}
