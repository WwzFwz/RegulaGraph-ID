// Revalidates registered RESOLVE candidate context at an explicit retained registry revision.
// One bounded historical alias read compares every positive/negative scope, complete alias
// payload and canonical profile, including unselected candidates. It never edits the original
// artifact or authorizes publication. Callers also authenticate the committed resolution,
// extraction dependencies, snapshot membership, and live publication fence. Measure batch SQL
// p95/p99, bytes and stale-context frequency under benchmark-targets.yaml (REQUIRED_UNMEASURED).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// VerifyRegistryCandidateView authenticates stored candidate/source bytes and checks that
// the model's candidate context is unchanged at targetRevision. This is not a decision
// reaffirmation receipt: changed context requires replan, even if the selected ID survives.
// The caller supplies the publication-bound target, not an arbitrary latest revision.
func (r *Repository) VerifyRegistryCandidateView(ctx context.Context, corpusID string,
	input domain.SemanticRegistryInputs, targetRevision uint64, maximumScopes,
	maximumAliasesPerScope, maximumReferences, maximumCandidatesPerMention int) error {
	if r == nil || r.pool == nil || ctx == nil || !storageIDPattern.MatchString(corpusID) ||
		targetRevision == 0 || targetRevision > math.MaxInt64 || maximumScopes <= 0 ||
		maximumAliasesPerScope <= 0 || maximumReferences <= 0 || maximumCandidatesPerMention <= 0 {
		return errors.New("registered candidate inputs and bounded target view required")
	}
	source, batch, err := r.decodeSemanticInputs(ctx, corpusID, input)
	if err != nil {
		return err
	}
	if err = domain.ValidateRegistryCandidateBatch(batch, source, input.SourceRef,
		maximumReferences, maximumCandidatesPerMention); err != nil {
		return err
	}
	if targetRevision < batch.RegistryRevision {
		return fmt.Errorf("target view predates candidate context: %w", domain.ErrResolutionReplan)
	}
	// Candidate validation checks all repeated observations agree. Deduplicate keys
	// before I/O, without dropping any mention's positive or negative dependency.
	seen := make(map[string]bool)
	scopes := make([]RegistryLookupScope, 0)
	for _, lookup := range batch.Lookups {
		for _, scope := range lookup.Scopes {
			if seen[scope.Revision.ScopeId] {
				continue
			}
			if len(scopes) >= maximumScopes {
				return ErrResultLimit
			}
			seen[scope.Revision.ScopeId] = true
			scopes = append(scopes, RegistryLookupScope{EntityType: scope.EntityType,
				CanonicalScope: scope.CanonicalScope, NormalizedLookup: scope.NormalizedLookup})
		}
	}
	if len(scopes) == 0 {
		return ErrNoCandidateMentions
	}
	results, _, err := r.LookupCanonicalAliasesAtRevision(ctx, corpusID, targetRevision,
		scopes, maximumScopes, maximumAliasesPerScope)
	if err != nil {
		return err
	}
	return compareRegistryCandidateView(batch, results)
}

// Inputs have passed domain validation and the authenticated historical reader.
// Equality is by stable ID, independent of serialization order; payloads and scope
// revisions remain exact. Alias revision changes trigger replan even for an ABA edit.
func compareRegistryCandidateView(batch *pb.RegistryCandidateBatch, results []RegistryLookupResult) error {
	changed := func(part string) error {
		return fmt.Errorf("registry candidate context changed (%s): %w", part, domain.ErrResolutionReplan)
	}
	entities := make(map[string]*pb.CanonicalEntity, len(batch.Candidates))
	aliases := make(map[string]*pb.Alias, len(batch.Aliases))
	for _, entity := range batch.Candidates {
		entities[entity.Meta.RecordId] = entity
	}
	for _, alias := range batch.Aliases {
		aliases[alias.Meta.RecordId] = alias
	}
	byScope := make(map[string]RegistryLookupResult, len(results))
	seenEntities, seenAliases := map[string]bool{}, map[string]bool{}
	for _, result := range results {
		key := domain.RegistryLookupScopeID(result.Scope.EntityType, result.Scope.CanonicalScope, result.Scope.NormalizedLookup)
		if result.Revision == nil || result.Revision.ScopeId != key {
			return changed("scope identity")
		}
		if _, exists := byScope[key]; exists {
			return changed("duplicate scope")
		}
		byScope[key] = result
		for _, entity := range result.Candidates {
			id := entity.GetMeta().GetRecordId()
			if entities[id] == nil || !proto.Equal(entities[id], entity) {
				return changed("canonical profile")
			}
			seenEntities[id] = true
		}
		for _, alias := range result.Aliases {
			id := alias.GetMeta().GetRecordId()
			if aliases[id] == nil || !proto.Equal(aliases[id], alias) {
				return changed("alias evidence")
			}
			seenAliases[id] = true
		}
	}
	seenScopes := make(map[string]bool)
	for _, lookup := range batch.Lookups {
		for _, scope := range lookup.Scopes {
			result, exists := byScope[scope.Revision.ScopeId]
			if !exists || !proto.Equal(scope.Revision, result.Revision) || len(scope.CandidateIds) != len(result.Candidates) {
				return changed("lookup observation")
			}
			ids := make(map[string]bool, len(result.Candidates))
			for _, entity := range result.Candidates {
				ids[entity.Meta.RecordId] = true
			}
			for _, id := range scope.CandidateIds {
				if !ids[id] {
					return changed("candidate membership")
				}
			}
			seenScopes[scope.Revision.ScopeId] = true
		}
	}
	if len(seenScopes) != len(byScope) || len(seenEntities) != len(entities) || len(seenAliases) != len(aliases) {
		return changed("lookup coverage")
	}
	return nil
}
