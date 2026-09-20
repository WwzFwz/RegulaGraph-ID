// Validates ExtractionBatch identity, accounting, provisional references, and evidence closure.
// This coordinator trust boundary mirrors the Rust assembler before immutable artifacts are
// registered. Chunk and version containment use sorted indexes; support closure is bounded by the
// configured edge limit and costs O(source refs x evidence spans). Model accuracy remains an
// evaluation concern; required performance targets are still unmeasured.
package domain

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ValidateExtractionBatchClosure rejects graph proposals that escape their source DocumentBatch.
func ValidateExtractionBatchClosure(batch *pb.ExtractionBatch, source *pb.DocumentBatch, maximumEdges int) error {
	if batch == nil || source == nil || batch.GetMeta() == nil || batch.GetContext() == nil ||
		batch.GetSourceDocumentBatch() == nil || batch.GetDependencies() == nil ||
		batch.GetModelManifest() == nil || batch.GetPromptHash() == nil || batch.GetItemCounts() == nil || maximumEdges <= 0 {
		return errors.New("extraction batch, source batch, and positive reference limit are required")
	}
	corpusID := batch.Meta.CorpusId
	if corpusID == "" || batch.Context.CorpusId != corpusID || source.GetMeta().GetCorpusId() != corpusID || source.GetContext().GetCorpusId() != corpusID {
		return errors.New("extraction and source corpus identities differ")
	}
	if batch.ModelManifest.Task != pb.ModelTask_MODEL_TASK_EXTRACT || !proto.Equal(batch.ModelManifest.PromptHash, batch.PromptHash) {
		return errors.New("extraction model task or prompt identity is invalid")
	}
	producer := batch.Dependencies.ProducerManifest
	if producer == nil || !containsModel(producer.Models, batch.ModelManifest) || !containsHash(producer.PromptHashes, batch.PromptHash) {
		return errors.New("extraction producer does not bind its model and prompt")
	}
	foundSourceDependency := false
	for _, dependency := range batch.Dependencies.Dependencies {
		if dependency != nil && dependency.DependencyId == batch.SourceDocumentBatch.ArtifactId &&
			proto.Equal(dependency.Fingerprint, batch.SourceDocumentBatch.ContentHash) {
			foundSourceDependency = true
		}
	}
	if !foundSourceDependency {
		return errors.New("extraction dependency manifest does not bind its source DocumentBatch")
	}
	counts := batch.ItemCounts
	if counts.Expected == 0 || counts.Accepted > counts.Expected || math.MaxUint64-counts.Accepted < counts.Rejected || counts.Accepted+counts.Rejected != counts.Expected {
		return errors.New("extraction item accounting is invalid")
	}
	expectedCompleteness := pb.Completeness_COMPLETENESS_PARTIAL
	if counts.Accepted == counts.Expected {
		expectedCompleteness = pb.Completeness_COMPLETENESS_COMPLETE
	} else if counts.Accepted == 0 {
		expectedCompleteness = pb.Completeness_COMPLETENESS_NONE
	}
	if batch.Completeness != expectedCompleteness {
		return errors.New("extraction completeness differs from item accounting")
	}
	if counts.Expected != uint64(len(source.Chunks)) {
		return errors.New("extraction expected count differs from source chunk count")
	}
	hasErrorIssue := false
	for _, issue := range batch.Issues {
		hasErrorIssue = hasErrorIssue || issue.GetSeverity() == pb.Severity_SEVERITY_ERROR
	}
	if (counts.Rejected > 0) != hasErrorIssue {
		return errors.New("extraction rejected count and error issues disagree")
	}

	texts := make(map[string]*pb.TextArtifact, len(source.TextArtifacts))
	for _, text := range source.TextArtifacts {
		texts[text.GetMeta().GetRecordId()] = text
	}
	chunks := make(map[string]*pb.Chunk, len(source.Chunks))
	for _, chunk := range source.Chunks {
		chunks[chunk.GetMeta().GetRecordId()] = chunk
	}
	chunkCoverage := buildSpanCoverage(source.Chunks)
	provisionRegulation := make(map[string]string, len(source.Provisions))
	for _, provision := range source.Provisions {
		provisionRegulation[provision.GetMeta().GetRecordId()] = provision.RegulationId
	}
	versions := make(map[string]*pb.ProvisionVersion, len(source.Versions))
	versionSpans := buildRecordSpanIndex(source.Versions, func(version *pb.ProvisionVersion) string { return version.GetMeta().GetRecordId() }, func(version *pb.ProvisionVersion) []*pb.TextSpan { return version.Spans })
	for _, version := range source.Versions {
		versions[version.GetMeta().GetRecordId()] = version
	}
	spanValid := func(span *pb.TextSpan) bool {
		if span == nil || span.StartByte >= span.EndByte {
			return false
		}
		text := texts[span.TextArtifactId]
		if text == nil || text.NormalizedTextRef == nil || span.EndByte > text.NormalizedTextRef.ByteSize {
			return false
		}
		return chunkCoverage.contains(span)
	}
	sourceRefSupports := func(reference *pb.SourceVersionRef, span *pb.TextSpan) bool {
		if reference == nil || span == nil {
			return false
		}
		version := versions[reference.ProvisionVersionId]
		text := texts[span.TextArtifactId]
		return version != nil && text != nil && text.SourceBlobId == reference.SourceBlobId &&
			provisionRegulation[version.ProvisionId] == reference.RegulationId && versionSpans.contains(reference.ProvisionVersionId, span)
	}

	recordIDs := map[string]bool{}
	mentions := make(map[string]bool, len(batch.Mentions))
	assertions := make(map[string]bool, len(batch.Assertions))
	edges := 0
	validationWork := 0
	addEdges := func(count int) error {
		if count < 0 || edges > maximumEdges-count {
			return fmt.Errorf("extraction reference count exceeds %d", maximumEdges)
		}
		edges += count
		return nil
	}
	addValidationProduct := func(left, right int) error {
		if left < 0 || right < 0 || left != 0 && right > maximumEdges/left || validationWork > maximumEdges-left*right {
			return fmt.Errorf("extraction evidence validation work exceeds %d", maximumEdges)
		}
		validationWork += left * right
		return nil
	}
	validateMeta := func(kind string, meta *pb.RecordMeta) error {
		if meta == nil || meta.CorpusId != corpusID || meta.RecordId == "" || meta.Visibility != nil {
			return fmt.Errorf("%s has invalid provisional identity", kind)
		}
		if recordIDs[meta.RecordId] {
			return fmt.Errorf("duplicate extraction record %q", meta.RecordId)
		}
		recordIDs[meta.RecordId] = true
		return nil
	}
	for _, mention := range batch.Mentions {
		if err := validateMeta("mention", mention.GetMeta()); err != nil {
			return err
		}
		if !proto.Equal(mention.ExtractionManifest, producer) || !spanValid(mention.TextSpan) || len(mention.SourceRefs) == 0 {
			return fmt.Errorf("mention %q has invalid manifest, span, or source refs", mention.Meta.RecordId)
		}
		seenRefs := map[string]bool{}
		for _, reference := range mention.SourceRefs {
			key := sourceRefKey(reference)
			if seenRefs[key] {
				return fmt.Errorf("mention %q repeats a source ref", mention.Meta.RecordId)
			}
			seenRefs[key] = true
			if !sourceRefSupports(reference, mention.TextSpan) {
				return fmt.Errorf("mention %q source ref does not support its span", mention.Meta.RecordId)
			}
		}
		mentions[mention.Meta.RecordId] = true
		if err := addEdges(1 + len(mention.SourceRefs)); err != nil {
			return err
		}
	}
	for _, assertion := range batch.Assertions {
		if err := validateMeta("assertion", assertion.GetMeta()); err != nil {
			return err
		}
		if assertion.OntologyVersion != batch.OntologyVersion {
			return fmt.Errorf("assertion %q uses a different ontology", assertion.Meta.RecordId)
		}
		assertions[assertion.Meta.RecordId] = true
	}
	for _, assertion := range batch.Assertions {
		if !mentions[assertion.SubjectId] || !mentions[assertion.ObjectId] {
			return fmt.Errorf("assertion %q references an unknown mention", assertion.Meta.RecordId)
		}
		for _, qualifier := range assertion.Qualifiers {
			switch value := qualifier.GetValue().(type) {
			case *pb.Qualifier_MentionId:
				if !mentions[value.MentionId] {
					return fmt.Errorf("assertion %q qualifier references an unknown mention", assertion.Meta.RecordId)
				}
			case *pb.Qualifier_CanonicalId:
				return fmt.Errorf("assertion %q contains canonical identity before RESOLVE", assertion.Meta.RecordId)
			case nil:
				return fmt.Errorf("assertion %q contains an empty qualifier", assertion.Meta.RecordId)
			}
		}
		for _, exception := range assertion.ExceptionRefs {
			if exception == assertion.Meta.RecordId || !assertions[exception] {
				return fmt.Errorf("assertion %q has an invalid exception reference", assertion.Meta.RecordId)
			}
		}
		if err := addEdges(2 + len(assertion.Qualifiers) + len(assertion.ExceptionRefs)); err != nil {
			return err
		}
	}
	supportCounts := make(map[string]int, len(assertions))
	for _, support := range batch.Supports {
		if err := validateMeta("support", support.GetMeta()); err != nil {
			return err
		}
		if !assertions[support.AssertionId] || !proto.Equal(support.ExtractionManifest, producer) || len(support.EvidenceSpans) == 0 || len(support.SourceRefs) == 0 {
			return fmt.Errorf("support %q has invalid assertion, manifest, spans, or source refs", support.Meta.RecordId)
		}
		if support.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED && support.ReviewState != pb.ReviewState_REVIEW_STATE_QUARANTINED {
			return fmt.Errorf("support %q has an invalid pre-resolution review state", support.Meta.RecordId)
		}
		if err := addValidationProduct(len(support.SourceRefs), len(support.EvidenceSpans)); err != nil {
			return err
		}
		seenRefs := map[string]bool{}
		for _, reference := range support.SourceRefs {
			key := sourceRefKey(reference)
			if seenRefs[key] {
				return fmt.Errorf("support %q repeats a source ref", support.Meta.RecordId)
			}
			seenRefs[key] = true
			supported := false
			for _, span := range support.EvidenceSpans {
				if spanValid(span) && sourceRefSupports(reference, span) {
					supported = true
				}
			}
			if !supported {
				return fmt.Errorf("support %q source ref does not support evidence", support.Meta.RecordId)
			}
		}
		for _, span := range support.EvidenceSpans {
			if !spanValid(span) {
				return fmt.Errorf("support %q has evidence outside source chunks", support.Meta.RecordId)
			}
			covered := false
			for _, reference := range support.SourceRefs {
				covered = covered || sourceRefSupports(reference, span)
			}
			if !covered {
				return fmt.Errorf("support %q has evidence without a matching source ref", support.Meta.RecordId)
			}
		}
		supportCounts[support.AssertionId]++
		if err := addEdges(1 + len(support.EvidenceSpans) + len(support.SourceRefs)); err != nil {
			return err
		}
	}
	for assertionID := range assertions {
		if supportCounts[assertionID] == 0 {
			return fmt.Errorf("assertion %q has no evidence support", assertionID)
		}
	}
	errorIssueRecords := map[string]bool{}
	for _, issue := range batch.Issues {
		if issue == nil || (!recordIDs[issue.RecordId] && chunks[issue.RecordId] == nil) {
			return errors.New("extraction issue references an unknown record")
		}
		if issue.Severity == pb.Severity_SEVERITY_ERROR {
			if chunks[issue.RecordId] == nil {
				return errors.New("extraction error issue must identify a rejected source chunk")
			}
			errorIssueRecords[issue.RecordId] = true
		}
		for _, evidenceRef := range issue.EvidenceRefs {
			if !recordIDs[evidenceRef] && chunks[evidenceRef] == nil {
				return fmt.Errorf("extraction issue references unknown evidence %q", evidenceRef)
			}
		}
		if err := addEdges(len(issue.EvidenceRefs)); err != nil {
			return err
		}
	}
	if uint64(len(errorIssueRecords)) != counts.Rejected {
		return errors.New("extraction rejected count differs from distinct failed items")
	}
	return nil
}

type textCoverage struct {
	ranges    []byteRange
	prefixMax []uint64
}

type spanCoverage map[string]textCoverage

func buildSpanCoverage(chunks []*pb.Chunk) spanCoverage {
	grouped := make(map[string][]byteRange)
	for _, chunk := range chunks {
		if span := chunk.GetTextSpan(); span != nil {
			grouped[span.TextArtifactId] = append(grouped[span.TextArtifactId], byteRange{start: span.StartByte, end: span.EndByte})
		}
	}
	result := make(spanCoverage, len(grouped))
	for textID, ranges := range grouped {
		sort.Slice(ranges, func(left, right int) bool { return ranges[left].start < ranges[right].start })
		prefix := make([]uint64, len(ranges))
		for index, span := range ranges {
			prefix[index] = span.end
			if index > 0 && prefix[index-1] > prefix[index] {
				prefix[index] = prefix[index-1]
			}
		}
		result[textID] = textCoverage{ranges: ranges, prefixMax: prefix}
	}
	return result
}

func (coverage spanCoverage) contains(span *pb.TextSpan) bool {
	if span == nil {
		return false
	}
	entry, ok := coverage[span.TextArtifactId]
	if !ok {
		return false
	}
	index := sort.Search(len(entry.ranges), func(index int) bool { return entry.ranges[index].start > span.StartByte }) - 1
	return index >= 0 && entry.prefixMax[index] >= span.EndByte
}

func sourceRefKey(reference *pb.SourceVersionRef) string {
	if reference == nil {
		return ""
	}
	return reference.SourceBlobId + "\x00" + reference.ProvisionVersionId + "\x00" + reference.RegulationId
}

func containsModel(values []*pb.ModelManifest, expected *pb.ModelManifest) bool {
	for _, value := range values {
		if proto.Equal(value, expected) {
			return true
		}
	}
	return false
}

func containsHash(values []*pb.ContentHash, expected *pb.ContentHash) bool {
	for _, value := range values {
		if proto.Equal(value, expected) {
			return true
		}
	}
	return false
}
