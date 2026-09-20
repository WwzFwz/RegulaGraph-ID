// Validates cross-record identity, reference closure, source spans, and structure cycles in DocumentBatch.
//
// Wire validation checks scalar shapes; this validator is the semantic trust boundary before an
// immutable worker artifact is registered. It performs bounded O(records + references) lookups and
// never opens storage or resolves external registries. References absent locally must be declared in
// the dependency manifest. Parent/reference traversal is linear; interval indexes cost O(S log S)
// for S source spans and make containment lookups logarithmic. Required performance gates remain in
// configs/benchmark-targets.yaml.
package domain

import (
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ValidateDocumentBatchClosure checks semantic links that protobuf field rules cannot express.
func ValidateDocumentBatchClosure(batch *pb.DocumentBatch, maximumEdges int) error {
	if batch == nil || batch.GetMeta() == nil || batch.GetDependencyManifest() == nil || maximumEdges <= 0 {
		return errors.New("document batch and a positive reference limit are required")
	}
	corpusID := batch.Meta.CorpusId
	dependencies := make(map[string]struct{}, len(batch.DependencyManifest.Dependencies))
	edges := 0
	addEdge := func(field, id string, exists bool) error {
		edges++
		if edges > maximumEdges {
			return fmt.Errorf("document reference count exceeds %d", maximumEdges)
		}
		if id == "" || !exists {
			return fmt.Errorf("%s references unavailable record %q", field, id)
		}
		return nil
	}
	for _, dependency := range batch.DependencyManifest.Dependencies {
		if dependency == nil || dependency.DependencyId == "" {
			return errors.New("dependency manifest contains an empty dependency")
		}
		if _, duplicate := dependencies[dependency.DependencyId]; duplicate {
			return fmt.Errorf("duplicate dependency %q", dependency.DependencyId)
		}
		dependencies[dependency.DependencyId] = struct{}{}
	}
	hasDependency := func(id string) bool { _, ok := dependencies[id]; return ok }

	sources := make(map[string]*pb.SourceBlob, len(batch.Sources))
	texts := make(map[string]*pb.TextArtifact, len(batch.TextArtifacts))
	structures := make(map[string]*pb.StructureNode, len(batch.Structures))
	provisions := make(map[string]*pb.Provision, len(batch.Provisions))
	versions := make(map[string]*pb.ProvisionVersion, len(batch.Versions))
	chunks := make(map[string]*pb.Chunk, len(batch.Chunks))
	regulations := make(map[string]*pb.Regulation, len(batch.Regulations))
	observations := make(map[string]*pb.SourceObservation, len(batch.Observations))
	editions := make(map[string]*pb.DocumentEdition, len(batch.Editions))
	changes := make(map[string]*pb.LegalChangeEvent, len(batch.Changes))

	addRecord := func(kind string, meta *pb.RecordMeta, seen map[string]struct{}) error {
		if meta == nil || meta.RecordId == "" || meta.CorpusId != corpusID {
			return fmt.Errorf("%s has missing identity or foreign corpus", kind)
		}
		if _, duplicate := seen[meta.RecordId]; duplicate {
			return fmt.Errorf("duplicate %s id %q", kind, meta.RecordId)
		}
		seen[meta.RecordId] = struct{}{}
		return nil
	}
	index := func(kind string, length int, meta func(int) *pb.RecordMeta) (map[string]struct{}, error) {
		seen := make(map[string]struct{}, length)
		for i := 0; i < length; i++ {
			if err := addRecord(kind, meta(i), seen); err != nil {
				return nil, err
			}
		}
		return seen, nil
	}
	if _, err := index("source", len(batch.Sources), func(i int) *pb.RecordMeta { return batch.Sources[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Sources {
		sources[record.Meta.RecordId] = record
	}
	if _, err := index("text artifact", len(batch.TextArtifacts), func(i int) *pb.RecordMeta { return batch.TextArtifacts[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.TextArtifacts {
		texts[record.Meta.RecordId] = record
	}
	if _, err := index("structure", len(batch.Structures), func(i int) *pb.RecordMeta { return batch.Structures[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Structures {
		structures[record.Meta.RecordId] = record
	}
	if _, err := index("provision", len(batch.Provisions), func(i int) *pb.RecordMeta { return batch.Provisions[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Provisions {
		provisions[record.Meta.RecordId] = record
	}
	if _, err := index("version", len(batch.Versions), func(i int) *pb.RecordMeta { return batch.Versions[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Versions {
		versions[record.Meta.RecordId] = record
	}
	if _, err := index("chunk", len(batch.Chunks), func(i int) *pb.RecordMeta { return batch.Chunks[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Chunks {
		chunks[record.Meta.RecordId] = record
	}
	if _, err := index("regulation", len(batch.Regulations), func(i int) *pb.RecordMeta { return batch.Regulations[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Regulations {
		regulations[record.Meta.RecordId] = record
	}
	if _, err := index("observation", len(batch.Observations), func(i int) *pb.RecordMeta { return batch.Observations[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Observations {
		observations[record.Meta.RecordId] = record
	}
	if _, err := index("edition", len(batch.Editions), func(i int) *pb.RecordMeta { return batch.Editions[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Editions {
		editions[record.Meta.RecordId] = record
	}
	if _, err := index("change", len(batch.Changes), func(i int) *pb.RecordMeta { return batch.Changes[i].GetMeta() }); err != nil {
		return err
	}
	for _, record := range batch.Changes {
		changes[record.Meta.RecordId] = record
	}

	localOrDependency := func(id string, local bool) bool { return local || hasDependency(id) }
	spanFits := func(span *pb.TextSpan) bool {
		if span == nil || span.StartByte > span.EndByte {
			return false
		}
		text := texts[span.TextArtifactId]
		return text != nil && text.GetNormalizedTextRef() != nil && span.EndByte <= text.NormalizedTextRef.ByteSize
	}

	for _, text := range batch.TextArtifacts {
		if err := addEdge("text_artifact.source_blob_id", text.SourceBlobId, sources[text.SourceBlobId] != nil); err != nil {
			return err
		}
		for _, page := range text.PageResults {
			for _, span := range page.Spans {
				if err := addEdge("text_artifact.page_results.spans", span.GetTextArtifactId(), spanFits(span) && span.TextArtifactId == text.Meta.RecordId); err != nil {
					return err
				}
			}
		}
	}
	parents := make(map[string]string, len(batch.Structures))
	listedParent := make(map[string]string, len(batch.Structures))
	for _, parent := range batch.Structures {
		for _, childID := range parent.OrderedChildren {
			if previous, duplicate := listedParent[childID]; duplicate && previous != parent.Meta.RecordId {
				return fmt.Errorf("structure %q is listed by multiple parents", childID)
			}
			listedParent[childID] = parent.Meta.RecordId
		}
	}
	for _, node := range batch.Structures {
		if node.ParentId != nil {
			parent := structures[node.GetParentId()]
			if err := addEdge("structure.parent_id", node.GetParentId(), parent != nil && listedParent[node.Meta.RecordId] == node.GetParentId()); err != nil {
				return err
			}
			parents[node.Meta.RecordId] = node.GetParentId()
		}
		for _, childID := range node.OrderedChildren {
			child := structures[childID]
			if err := addEdge("structure.ordered_children", childID, child != nil && child.GetParentId() == node.Meta.RecordId); err != nil {
				return err
			}
		}
		for _, span := range node.SourceSpans {
			if err := addEdge("structure.source_spans", span.GetTextArtifactId(), spanFits(span)); err != nil {
				return err
			}
		}
		for _, locator := range node.PageLocators {
			if err := addEdge("structure.page_locators", locator.GetSourceBlobId(), localOrDependency(locator.GetSourceBlobId(), sources[locator.GetSourceBlobId()] != nil)); err != nil {
				return err
			}
		}
	}
	if err := validateStructureParents(parents, len(structures)); err != nil {
		return err
	}
	structureSpans := buildRecordSpanIndex(batch.Structures, func(node *pb.StructureNode) string { return node.Meta.RecordId }, func(node *pb.StructureNode) []*pb.TextSpan { return node.SourceSpans })

	for _, regulation := range batch.Regulations {
		if err := addEdge("regulation.issuer_id", regulation.IssuerId, hasDependency(regulation.IssuerId)); err != nil {
			return err
		}
	}
	for _, provision := range batch.Provisions {
		if err := addEdge("provision.regulation_id", provision.RegulationId, regulations[provision.RegulationId] != nil); err != nil {
			return err
		}
		if provision.ParentProvisionId != nil {
			id := provision.GetParentProvisionId()
			if err := addEdge("provision.parent_provision_id", id, provisions[id] != nil); err != nil {
				return err
			}
		}
		for _, id := range provision.LineageRefs {
			if err := addEdge("provision.lineage_refs", id, localOrDependency(id, provisions[id] != nil)); err != nil {
				return err
			}
		}
	}
	for _, version := range batch.Versions {
		if err := addEdge("version.provision_id", version.ProvisionId, provisions[version.ProvisionId] != nil); err != nil {
			return err
		}
		var owningText *pb.TextArtifact
		for _, span := range version.Spans {
			if err := addEdge("version.spans", span.GetTextArtifactId(), spanFits(span)); err != nil {
				return err
			}
			if owningText == nil {
				owningText = texts[span.TextArtifactId]
			} else if owningText.Meta.RecordId != span.TextArtifactId {
				return errors.New("version spans cross text artifacts")
			}
		}
		if owningText == nil || !proto.Equal(version.TextRef, owningText.NormalizedTextRef) {
			return fmt.Errorf("version %q text_ref does not match its normalized artifact", version.Meta.RecordId)
		}
		for _, id := range version.SupportingEvents {
			if err := addEdge("version.supporting_events", id, localOrDependency(id, changes[id] != nil)); err != nil {
				return err
			}
		}
	}
	versionSpans := buildRecordSpanIndex(batch.Versions, func(version *pb.ProvisionVersion) string { return version.Meta.RecordId }, func(version *pb.ProvisionVersion) []*pb.TextSpan { return version.Spans })
	batchTokenizerID := ""
	for _, chunk := range batch.Chunks {
		for _, id := range chunk.ProvisionVersionRefs {
			if err := addEdge("chunk.provision_version_refs", id, versions[id] != nil && versionSpans.contains(id, chunk.TextSpan)); err != nil {
				return err
			}
		}
		if chunk.TextSpan == nil || chunk.TextSpan.StartByte >= chunk.TextSpan.EndByte {
			return fmt.Errorf("chunk %q has an empty text span", chunk.Meta.RecordId)
		}
		if len(chunk.TokenCounts) != 1 || chunk.TokenCounts[0] == nil || chunk.TokenCounts[0].InputTokens == 0 || chunk.TokenCounts[0].OutputTokens != 0 {
			return fmt.Errorf("chunk %q must carry exactly one positive input token count", chunk.Meta.RecordId)
		}
		if batchTokenizerID == "" {
			batchTokenizerID = chunk.TokenCounts[0].TokenizerId
		} else if batchTokenizerID != chunk.TokenCounts[0].TokenizerId {
			return fmt.Errorf("chunk %q uses a tokenizer inconsistent with its batch", chunk.Meta.RecordId)
		}
		if err := addEdge("chunk.text_span", chunk.TextSpan.TextArtifactId, spanFits(chunk.TextSpan)); err != nil {
			return err
		}
		for _, id := range append(append([]string{}, chunk.StructureNodeRefs...), chunk.ParentRefs...) {
			if err := addEdge("chunk.structure_refs", id, structures[id] != nil && structureSpans.contains(id, chunk.TextSpan)); err != nil {
				return err
			}
		}
		for _, id := range chunk.ExceptionRefs {
			if err := addEdge("chunk.exception_refs", id, localOrDependency(id, chunks[id] != nil)); err != nil {
				return err
			}
		}
	}
	for _, edition := range batch.Editions {
		if edition.RegulationId != nil {
			id := edition.GetRegulationId()
			if err := addEdge("edition.regulation_id", id, localOrDependency(id, regulations[id] != nil)); err != nil {
				return err
			}
		}
		for _, id := range edition.SourceRefs {
			if err := addEdge("edition.source_refs", id, localOrDependency(id, observations[id] != nil)); err != nil {
				return err
			}
		}
	}
	for _, observation := range batch.Observations {
		if observation.SourceBlobId != nil {
			id := observation.GetSourceBlobId()
			if err := addEdge("observation.source_blob_id", id, localOrDependency(id, sources[id] != nil)); err != nil {
				return err
			}
		}
	}
	for _, change := range batch.Changes {
		if change.AmendingSource == nil {
			return errors.New("change has no amending source")
		}
		if err := addEdge("change.amending_source.source_blob_id", change.AmendingSource.SourceBlobId, localOrDependency(change.AmendingSource.SourceBlobId, sources[change.AmendingSource.SourceBlobId] != nil)); err != nil {
			return err
		}
		if err := addEdge("change.amending_source.provision_version_id", change.AmendingSource.ProvisionVersionId, localOrDependency(change.AmendingSource.ProvisionVersionId, versions[change.AmendingSource.ProvisionVersionId] != nil)); err != nil {
			return err
		}
		if err := addEdge("change.amending_source.regulation_id", change.AmendingSource.RegulationId, localOrDependency(change.AmendingSource.RegulationId, regulations[change.AmendingSource.RegulationId] != nil)); err != nil {
			return err
		}
		for _, id := range change.AffectedProvisions {
			if err := addEdge("change.affected_provisions", id, localOrDependency(id, provisions[id] != nil)); err != nil {
				return err
			}
		}
		for _, span := range change.ReplacementSpans {
			if err := addEdge("change.replacement_spans", span.GetTextArtifactId(), spanFits(span)); err != nil {
				return err
			}
		}
	}
	return nil
}

type byteRange struct{ start, end uint64 }

type recordSpanIndex map[string]map[string][]byteRange

func buildRecordSpanIndex[T any](records []T, id func(T) string, spans func(T) []*pb.TextSpan) recordSpanIndex {
	index := make(recordSpanIndex, len(records))
	for _, record := range records {
		byText := make(map[string][]byteRange)
		for _, span := range spans(record) {
			if span != nil {
				byText[span.TextArtifactId] = append(byText[span.TextArtifactId], byteRange{span.StartByte, span.EndByte})
			}
		}
		for textID := range byText {
			sort.Slice(byText[textID], func(left, right int) bool { return byText[textID][left].start < byText[textID][right].start })
		}
		index[id(record)] = byText
	}
	return index
}

func (index recordSpanIndex) contains(recordID string, span *pb.TextSpan) bool {
	if span == nil {
		return false
	}
	ranges := index[recordID][span.TextArtifactId]
	position := sort.Search(len(ranges), func(i int) bool { return ranges[i].start > span.StartByte })
	if position == 0 {
		return false
	}
	candidate := ranges[position-1]
	return candidate.start <= span.StartByte && span.EndByte <= candidate.end
}

func validateStructureParents(parents map[string]string, maximum int) error {
	states := make(map[string]uint8, len(parents))
	for start := range parents {
		if states[start] == 2 {
			continue
		}
		path := make([]string, 0, 16)
		for current := start; current != ""; current = parents[current] {
			if states[current] == 2 {
				break
			}
			if states[current] == 1 {
				return errors.New("structure parent graph contains a cycle")
			}
			states[current] = 1
			path = append(path, current)
			if len(path) > maximum {
				return errors.New("structure parent graph exceeds its record bound")
			}
		}
		for _, id := range path {
			states[id] = 2
		}
	}
	return nil
}
