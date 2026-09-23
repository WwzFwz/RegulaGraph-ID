// Checks a read-only registry candidate artifact before RESOLVE dispatch.
// Go assembles one revision-pinned batch and Rust consumes it without per-mention registry RPCs.
// Every extracted mention has a lookup, including empty results; dependency revisions must be
// identical to those lookup observations. Positive results require a sourced alias with the
// same canonical ID, scope, and normalized key; scope IDs are recomputed from those keys.
// This is structural provenance, not a semantic match judgment or a database receipt.
// Candidate count, total references, and wire bytes must be
// bounded before persistence; quality/latency targets remain REQUIRED_UNMEASURED.
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type candidateLookupKey struct {
	entityType       string
	canonicalScope   string
	normalizedLookup string
}

// RegistryLookupScopeID is the C01 identity of one exact alias lookup key. PostgreSQL and
// artifact validation share this implementation so an arbitrary scope ID cannot disguise
// a changed type, legal scope, or normalized string.
func RegistryLookupScopeID(entityType, scope, normalized string) string {
	hasher := sha256.New()
	for _, part := range []string{"registry-alias-lookup:v1", entityType, scope, normalized} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		hasher.Write(size[:])
		hasher.Write([]byte(part))
	}
	return "lookup:" + hex.EncodeToString(hasher.Sum(nil))
}

// ValidateRegistryCandidateBatch checks the complete EXTRACT-to-RESOLVE candidate handoff.
func ValidateRegistryCandidateBatch(batch *pb.RegistryCandidateBatch, source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef, maximumReferences, maximumCandidatesPerMention int) error {
	if batch == nil || source == nil || sourceRef == nil || batch.Meta == nil || batch.Context == nil ||
		batch.SourceExtractionBatch == nil || batch.Dependencies == nil || source.Meta == nil || source.Context == nil ||
		maximumReferences <= 0 || maximumCandidatesPerMention <= 0 {
		return errors.New("candidate batch, source, ref, and positive limits are required")
	}
	if batch.Meta.RecordId == "" || batch.Meta.CorpusId == "" || batch.Meta.CorpusId != source.Meta.CorpusId ||
		batch.Meta.SchemaVersion == 0 || batch.Meta.SchemaVersion != source.Meta.SchemaVersion ||
		batch.Meta.SchemaVersion != batch.Context.SchemaVersion ||
		batch.Context.CorpusId != source.Context.CorpusId || batch.Meta.CorpusId != batch.Context.CorpusId ||
		batch.Context.SchemaVersion != source.Context.SchemaVersion ||
		batch.Context.AuthScopeRef == "" || batch.Context.AuthScopeRef != source.Context.AuthScopeRef ||
		batch.Context.ConfigFingerprint == nil || !proto.Equal(batch.Context.ConfigFingerprint, source.Context.ConfigFingerprint) ||
		!proto.Equal(batch.Context.SnapshotRef, source.Context.SnapshotRef) || batch.RegistryRevision == 0 ||
		source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		batch.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return errors.New("candidate batch context, registry revision, or completeness is invalid")
	}
	if !proto.Equal(batch.SourceExtractionBatch, sourceRef) || sourceRef.ArtifactId == "" || sourceRef.ContentHash == nil {
		return errors.New("candidate batch source ref differs from verified extraction artifact")
	}
	if len(batch.Lookups) != len(source.Mentions) || len(batch.Lookups) > maximumReferences ||
		len(batch.Candidates) > maximumReferences || len(batch.Aliases) > maximumReferences {
		return errors.New("candidate batch size or mention coverage is invalid")
	}
	manifest := batch.Dependencies
	if manifest.ProducerManifest == nil || len(manifest.Dependencies) > maximumReferences ||
		len(manifest.LookupScopeRevisions) > maximumReferences {
		return errors.New("candidate batch producer manifest is required")
	}
	work := 0
	addWork := func(amount int) error {
		if amount < 0 || work > maximumReferences-amount {
			return errors.New("candidate reference work exceeds configured limit")
		}
		work += amount
		return nil
	}
	if err := addWork(len(batch.Lookups) + len(batch.Candidates) + len(batch.Aliases) +
		len(manifest.Dependencies) + len(manifest.LookupScopeRevisions)); err != nil {
		return err
	}
	boundSource := false
	for _, dependency := range manifest.Dependencies {
		if dependency != nil && dependency.DependencyId == sourceRef.ArtifactId &&
			proto.Equal(dependency.Fingerprint, sourceRef.ContentHash) {
			boundSource = true
		}
	}
	if !boundSource {
		return errors.New("candidate batch dependency does not bind extraction input")
	}
	mentions := make(map[string]*pb.Mention, len(source.Mentions))
	for _, mention := range source.Mentions {
		id := mention.GetMeta().GetRecordId()
		if id == "" || mentions[id] != nil {
			return errors.New("extraction source has duplicate or missing mention identity")
		}
		mentions[id] = mention
	}
	candidates := make(map[string]*pb.CanonicalEntity, len(batch.Candidates))
	used := map[string]bool{}
	for _, candidate := range batch.Candidates {
		if candidate == nil || len(candidate.IdentityKeys) > maximumReferences {
			return errors.New("candidate identity keys exceed configured limit")
		}
		if err := addWork(len(candidate.IdentityKeys)); err != nil {
			return err
		}
		id := candidate.GetMeta().GetRecordId()
		if id == "" || candidates[id] != nil || candidate.GetMeta().GetCorpusId() != batch.Meta.CorpusId ||
			candidate.RegistryRevision == 0 || candidate.RegistryRevision > batch.RegistryRevision ||
			candidate.EntityType == "" || candidate.Scope == "" ||
			candidate.ReviewState != pb.ReviewState_REVIEW_STATE_APPROVED &&
				candidate.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED {
			return errors.New("candidate identity, type, scope, or registry revision is invalid")
		}
		candidates[id] = candidate
	}
	aliases := map[string]bool{}
	for _, alias := range batch.Aliases {
		if alias == nil || len(alias.SupportRefs) > maximumReferences {
			return errors.New("candidate alias support exceeds configured limit")
		}
		if err := addWork(len(alias.SupportRefs)); err != nil {
			return err
		}
		id := alias.GetMeta().GetRecordId()
		owner := candidates[alias.GetCanonicalId()]
		if id == "" || aliases[id] || candidates[id] != nil || owner == nil ||
			alias.GetMeta().GetCorpusId() != batch.Meta.CorpusId || alias.Scope != owner.Scope ||
			alias.Surface == "" || alias.NormalizedLookup == "" || len(alias.SupportRefs) == 0 {
			return errors.New("candidate alias has invalid identity, owner, scope, or support")
		}
		aliases[id] = true
	}
	observedScopes := map[string]*pb.LookupScopeRevision{}
	observedKeys := map[string]candidateLookupKey{}
	observedScopeIDs := map[candidateLookupKey]string{}
	observedResults := map[string]map[string]bool{}
	seenMentions := map[string]bool{}
	for _, lookup := range batch.Lookups {
		mention := mentions[lookup.GetMentionId()]
		if mention == nil || seenMentions[lookup.MentionId] || len(lookup.Scopes) == 0 ||
			len(lookup.Scopes) > maximumReferences {
			return errors.New("candidate lookup has missing, repeated, or over-limit mention")
		}
		seenMentions[lookup.MentionId] = true
		if err := addWork(len(lookup.Scopes)); err != nil {
			return err
		}
		seenCandidates := map[string]bool{}
		seenScopes := map[string]bool{}
		candidateCount := 0
		for _, scope := range lookup.Scopes {
			revision := scope.GetRevision()
			if revision == nil || revision.ScopeId == "" ||
				revision.ScopeId != RegistryLookupScopeID(scope.EntityType, scope.CanonicalScope, scope.NormalizedLookup) ||
				revision.Revision > batch.RegistryRevision ||
				seenScopes[revision.ScopeId] || scope.EntityType != mention.CandidateType ||
				scope.CanonicalScope == "" || scope.NormalizedLookup == "" ||
				len(scope.CandidateIds) > maximumCandidatesPerMention-candidateCount ||
				revision.EmptyResult != (len(scope.CandidateIds) == 0) {
				return fmt.Errorf("lookup for %q has invalid scope revision", lookup.MentionId)
			}
			seenScopes[revision.ScopeId] = true
			candidateCount += len(scope.CandidateIds)
			if err := addWork(len(scope.CandidateIds)); err != nil {
				return err
			}
			for _, id := range scope.CandidateIds {
				candidate := candidates[id]
				if candidate == nil || seenCandidates[id] || candidate.EntityType != scope.EntityType ||
					candidate.Scope != scope.CanonicalScope {
					return fmt.Errorf("lookup for %q has unknown, repeated, or wrong-scope candidate", lookup.MentionId)
				}
				seenCandidates[id] = true
				used[id] = true
			}
			key := candidateLookupKey{scope.EntityType, scope.CanonicalScope, scope.NormalizedLookup}
			if prior := observedScopes[revision.ScopeId]; prior != nil && !proto.Equal(prior, revision) {
				return errors.New("candidate lookup scope changed within one registry snapshot")
			}
			if prior, exists := observedKeys[revision.ScopeId]; exists && prior != key {
				return errors.New("candidate lookup scope key changed within one registry snapshot")
			}
			if prior, exists := observedScopeIDs[key]; exists && prior != revision.ScopeId {
				return errors.New("candidate lookup key has multiple scope IDs within one registry snapshot")
			}
			if prior, exists := observedResults[revision.ScopeId]; exists {
				if len(prior) != len(scope.CandidateIds) {
					return errors.New("candidate lookup result changed within one registry snapshot")
				}
				for _, id := range scope.CandidateIds {
					if !prior[id] {
						return errors.New("candidate lookup result changed within one registry snapshot")
					}
				}
			} else {
				result := make(map[string]bool, len(scope.CandidateIds))
				for _, id := range scope.CandidateIds {
					result[id] = true
				}
				observedResults[revision.ScopeId] = result
			}
			observedScopes[revision.ScopeId] = revision
			observedKeys[revision.ScopeId] = key
			observedScopeIDs[key] = revision.ScopeId
		}
	}
	for id := range candidates {
		if !used[id] {
			return fmt.Errorf("candidate %q has no lookup reference", id)
		}
	}
	coveredByAlias := make(map[string]map[string]bool, len(observedResults))
	for _, alias := range batch.Aliases {
		owner := candidates[alias.CanonicalId]
		key := candidateLookupKey{owner.EntityType, alias.Scope, alias.NormalizedLookup}
		scopeID, exists := observedScopeIDs[key]
		if !exists || !observedResults[scopeID][alias.CanonicalId] {
			return fmt.Errorf("candidate alias %q is not returned by its lookup scope", alias.Meta.RecordId)
		}
		if coveredByAlias[scopeID] == nil {
			coveredByAlias[scopeID] = make(map[string]bool)
		}
		coveredByAlias[scopeID][alias.CanonicalId] = true
	}
	for scopeID, ids := range observedResults {
		for id := range ids {
			if !coveredByAlias[scopeID][id] {
				return fmt.Errorf("candidate %q has no sourced alias in lookup %q", id, scopeID)
			}
		}
	}
	if len(manifest.LookupScopeRevisions) != len(observedScopes) {
		return errors.New("candidate dependency manifest omits or adds lookup scopes")
	}
	seenManifestScopes := map[string]bool{}
	for _, revision := range manifest.LookupScopeRevisions {
		if revision == nil || seenManifestScopes[revision.ScopeId] ||
			!proto.Equal(observedScopes[revision.ScopeId], revision) {
			return errors.New("candidate dependency scope revision differs from lookup result")
		}
		seenManifestScopes[revision.ScopeId] = true
	}
	return nil
}
