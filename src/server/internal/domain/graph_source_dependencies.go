// Shares graph source dependency and canonical-selection checks between workflow
// preparation and durable storage admission. Inputs have passed wire/closure
// validation; no storage reads or model decisions happen here. Unknown dependencies
// and unresolved decisions must not be dropped to make a source schedulable.
// Measure rejection counts and bounded planning p95 under benchmark-targets.yaml.
package domain

import (
	"errors"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"sort"
)

var ErrGraphAssemblyUnresolved = errors.New("ASSEMBLE requires resolved LINK/CREATE assignments; source needs resolution")

// BudgetGraphAssemblyTexts counts the same normalized texts read by the worker.
// The caller has validated document/extraction closure before entering this helper.
func BudgetGraphAssemblyTexts(document *pb.DocumentBatch, extraction *pb.ExtractionBatch, remaining *uint64) error {
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

func GraphAssemblyCanonicalSelection(resolution *pb.ResolutionBatch) ([]string, error) {
	if resolution == nil {
		return nil, errors.New("resolution required")
	}
	ids := map[string]bool{}
	for _, decision := range resolution.Decisions {
		if decision == nil || (decision.Action != pb.ResolutionAction_RESOLUTION_ACTION_LINK && decision.Action != pb.ResolutionAction_RESOLUTION_ACTION_CREATE) || len(decision.AssignedCanonicalIds) != 1 {
			return nil, ErrGraphAssemblyUnresolved
		}
		ids[decision.AssignedCanonicalIds[0]] = true
	}
	selected := make([]string, 0, len(ids))
	for id := range ids {
		selected = append(selected, id)
	}
	sort.Strings(selected)
	return selected, nil
}

func ValidateGraphExtractionDependencies(extraction *pb.ExtractionBatch) error {
	if extraction == nil || extraction.Dependencies == nil || extraction.SourceDocumentBatch == nil {
		return errors.New("graph extraction source dependencies required")
	}
	deps := extraction.Dependencies
	if len(deps.LookupScopeRevisions) != 0 || len(deps.Dependencies) != 1 || deps.Dependencies[0].GetDependencyId() != extraction.SourceDocumentBatch.ArtifactId || !proto.Equal(deps.Dependencies[0].GetFingerprint(), extraction.SourceDocumentBatch.ContentHash) {
		return errors.New("graph EXTRACT dependency contract is unsupported or incomplete")
	}
	return nil
}

// GraphCandidateAliasBudget preserves all scopes and the lookup reader's total
// result cap; it never permits a partial candidate set to compare successfully.
func GraphCandidateAliasBudget(candidates *pb.RegistryCandidateBatch, maximumEdges int) (int, error) {
	if candidates == nil || maximumEdges <= 0 {
		return 0, errors.New("bounded candidate view required")
	}
	scopes := map[string]bool{}
	for _, lookup := range candidates.Lookups {
		for _, scope := range lookup.Scopes {
			scopes[scope.Revision.ScopeId] = true
		}
	}
	if len(scopes) == 0 || len(scopes) > MaximumRegistryLookupAliases {
		return 0, errors.New("graph candidate lookup exceeds aggregate scope budget")
	}
	return min(maximumEdges, MaximumRegistryLookupAliases/len(scopes)), nil
}
