// Produces one revision-pinned candidate artifact from a batched PostgreSQL alias read.
// The caller supplies legal type/scope/normalization for each mention; this adapter does not
// infer identity from a surface string. It deduplicates lookup keys before one read-only
// transaction and passes the observations to the domain C01 builder. Measure query p95/p99,
// pool wait, bytes, and candidate coverage on gold; targets remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// ErrNoCandidateMentions tells the workflow to skip RESOLVE for a complete EXTRACT batch
// with no mentions; no registry read or empty synthetic revision is performed.
var ErrNoCandidateMentions = errors.New("complete extraction has no mentions to resolve")

// RegistryCandidatePlan is an explicit legal lookup plan for one extracted mention. Scope
// selection belongs to RESOLVE policy; this adapter only executes and binds the exact keys.
type RegistryCandidatePlan struct {
	MentionID string
	Scopes    []RegistryLookupScope
}

// PrepareRegistryCandidateBatch performs one batched lookup in one repeatable-read snapshot.
// The resulting batch remains an immutable artifact proposal until the workflow verifies its
// source EXTRACT artifact and persists it with the job fence.
func (r *Repository) PrepareRegistryCandidateBatch(ctx context.Context, source *pb.ExtractionBatch,
	sourceRef *pb.ArtifactRef, producer *pb.ProducerManifest, recordID string,
	plans []RegistryCandidatePlan, maximumScopes, maximumAliasesPerScope,
	maximumReferences, maximumCandidatesPerMention int) (*pb.RegistryCandidateBatch, error) {
	if ctx == nil || r == nil || source == nil || source.Meta == nil || source.Context == nil ||
		sourceRef == nil || sourceRef.ContentHash == nil || producer == nil ||
		source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		source.Meta.CorpusId == "" || source.Meta.CorpusId != source.Context.CorpusId ||
		len(plans) != len(source.Mentions) || len(plans) > maximumReferences || maximumScopes <= 0 ||
		maximumAliasesPerScope <= 0 || maximumReferences <= 0 || maximumCandidatesPerMention <= 0 {
		return nil, errors.New("complete extraction and bounded candidate plan are required")
	}
	for _, input := range []proto.Message{source.Meta, source.Context, sourceRef, producer} {
		if err := domain.ValidateWire(input, domain.DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("invalid candidate read input: %w", err)
		}
	}
	if len(source.Mentions) == 0 {
		return nil, ErrNoCandidateMentions
	}
	mentions := make(map[string]*pb.Mention, len(source.Mentions))
	for _, mention := range source.Mentions {
		id := mention.GetMeta().GetRecordId()
		if id == "" || mentions[id] != nil {
			return nil, errors.New("extraction mentions have duplicate or missing IDs")
		}
		mentions[id] = mention
	}
	seenPlans := make(map[string]bool, len(plans))
	unique := make(map[RegistryLookupScope]bool)
	totalScopes := 0
	for _, plan := range plans {
		mention := mentions[plan.MentionID]
		if mention == nil || seenPlans[plan.MentionID] || len(plan.Scopes) == 0 {
			return nil, errors.New("candidate plan omits or repeats a mention")
		}
		if len(plan.Scopes) > maximumReferences-totalScopes {
			return nil, errors.New("candidate plan exceeds total scope budget")
		}
		totalScopes += len(plan.Scopes)
		seenPlans[plan.MentionID] = true
		seenScopes := make(map[RegistryLookupScope]bool, len(plan.Scopes))
		for _, scope := range plan.Scopes {
			if scope.EntityType != mention.CandidateType || aliasEntityType(scope.EntityType) == 0 ||
				!validRegistryLookupText(scope.CanonicalScope) || !validRegistryLookupText(scope.NormalizedLookup) ||
				seenScopes[scope] {
				return nil, fmt.Errorf("invalid candidate scope for mention %q", plan.MentionID)
			}
			seenScopes[scope] = true
			unique[scope] = true
			if len(unique) > maximumScopes {
				return nil, errors.New("candidate lookup scope cap exceeded")
			}
		}
	}
	if len(unique) == 0 {
		return nil, errors.New("candidate lookup requires at least one scope")
	}
	ordered := make([]RegistryLookupScope, 0, len(unique))
	for scope := range unique {
		ordered = append(ordered, scope)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].EntityType != ordered[j].EntityType {
			return ordered[i].EntityType < ordered[j].EntityType
		}
		if ordered[i].CanonicalScope != ordered[j].CanonicalScope {
			return ordered[i].CanonicalScope < ordered[j].CanonicalScope
		}
		return ordered[i].NormalizedLookup < ordered[j].NormalizedLookup
	})
	results, revision, err := r.LookupCanonicalAliases(ctx, source.Meta.CorpusId, ordered,
		maximumScopes, maximumAliasesPerScope)
	if err != nil {
		return nil, err
	}
	return assembleRegistryCandidateResults(source, sourceRef, producer, recordID, plans,
		ordered, results, revision, maximumReferences, maximumCandidatesPerMention)
}

func assembleRegistryCandidateResults(source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef,
	producer *pb.ProducerManifest, recordID string, plans []RegistryCandidatePlan,
	ordered []RegistryLookupScope, results []RegistryLookupResult, revision uint64,
	maximumReferences, maximumCandidatesPerMention int) (*pb.RegistryCandidateBatch, error) {
	if len(ordered) != len(results) {
		return nil, errors.New("registry lookup response count differs from requested scopes")
	}
	byScope := make(map[RegistryLookupScope]RegistryLookupResult, len(results))
	entities := make(map[string]*pb.CanonicalEntity)
	aliases := make(map[string]*pb.Alias)
	for i, result := range results {
		if result.Scope != ordered[i] || result.Revision == nil ||
			result.Revision.ScopeId != registryLookupScopeID(result.Scope.EntityType,
				result.Scope.CanonicalScope, result.Scope.NormalizedLookup) ||
			result.Revision.Revision > revision || result.Revision.EmptyResult != (len(result.Candidates) == 0) {
			return nil, errors.New("registry lookup result key or revision differs from read snapshot")
		}
		if len(result.Candidates) > 0 && (result.Revision.Revision == 0 || len(result.Aliases) == 0) {
			return nil, errors.New("positive registry lookup lacks an alias or scope revision")
		}
		if _, duplicate := byScope[result.Scope]; duplicate {
			return nil, errors.New("registry lookup response repeats a scope")
		}
		byScope[result.Scope] = result
		scopeCandidates := make(map[string]bool, len(result.Candidates))
		coveredByAlias := make(map[string]bool, len(result.Candidates))
		for _, entity := range result.Candidates {
			id := entity.GetMeta().GetRecordId()
			if id == "" || scopeCandidates[id] || entity.EntityType != result.Scope.EntityType ||
				entity.Scope != result.Scope.CanonicalScope ||
				entities[id] != nil && !proto.Equal(entities[id], entity) {
				return nil, errors.New("registry returned conflicting canonical profile")
			}
			scopeCandidates[id] = true
			entities[id] = entity
		}
		seenAliases := make(map[string]bool, len(result.Aliases))
		for _, alias := range result.Aliases {
			id := alias.GetMeta().GetRecordId()
			if id == "" || seenAliases[id] || !scopeCandidates[alias.CanonicalId] ||
				alias.Scope != result.Scope.CanonicalScope || alias.NormalizedLookup != result.Scope.NormalizedLookup ||
				aliases[id] != nil && !proto.Equal(aliases[id], alias) {
				return nil, errors.New("registry returned conflicting alias record")
			}
			seenAliases[id] = true
			coveredByAlias[alias.CanonicalId] = true
			aliases[id] = alias
		}
		for id := range scopeCandidates {
			if !coveredByAlias[id] {
				return nil, errors.New("registry candidate has no matching alias row")
			}
		}
	}
	lookups := make([]*pb.CandidateLookup, 0, len(plans))
	for _, plan := range plans {
		lookup := &pb.CandidateLookup{MentionId: plan.MentionID}
		for _, scope := range plan.Scopes {
			result, exists := byScope[scope]
			if !exists {
				return nil, errors.New("candidate plan references unread registry scope")
			}
			ids := make([]string, 0, len(result.Candidates))
			for _, entity := range result.Candidates {
				ids = append(ids, entity.GetMeta().GetRecordId())
			}
			lookup.Scopes = append(lookup.Scopes, &pb.CandidateLookupScope{
				Revision:   proto.Clone(result.Revision).(*pb.LookupScopeRevision),
				EntityType: scope.EntityType, CanonicalScope: scope.CanonicalScope,
				NormalizedLookup: scope.NormalizedLookup, CandidateIds: ids,
			})
		}
		lookups = append(lookups, lookup)
	}
	entityList := make([]*pb.CanonicalEntity, 0, len(entities))
	for _, entity := range entities {
		entityList = append(entityList, entity)
	}
	aliasList := make([]*pb.Alias, 0, len(aliases))
	for _, alias := range aliases {
		aliasList = append(aliasList, alias)
	}
	return domain.AssembleRegistryCandidateBatch(source, sourceRef, producer, recordID, revision,
		lookups, entityList, aliasList, maximumReferences, maximumCandidatesPerMention)
}
