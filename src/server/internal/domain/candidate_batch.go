// Assembles a deterministic, bounded RegistryCandidateBatch from one trusted registry read.
// This pure Go helper binds EXTRACT bytes and every positive/negative lookup revision without
// selecting legal scope or deciding entity identity. The caller must obtain all observations
// from one PostgreSQL revision and verify storage receipts before dispatching RESOLVE.
// Sorting and validation cost O(n log n) over bounded records; candidate quality and p95/p99
// remain REQUIRED_UNMEASURED under configs/benchmark-targets.yaml.
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// AssembleRegistryCandidateBatch copies caller-owned records and derives the dependency
// manifest from the actual lookup observations. It never queries the registry itself.
func AssembleRegistryCandidateBatch(source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef,
	producer *pb.ProducerManifest, recordID string, registryRevision uint64,
	lookups []*pb.CandidateLookup, candidates []*pb.CanonicalEntity, aliases []*pb.Alias,
	maximumReferences, maximumCandidatesPerMention int) (*pb.RegistryCandidateBatch, error) {
	if source == nil || source.Meta == nil || source.Context == nil || sourceRef == nil || sourceRef.ContentHash == nil ||
		producer == nil || registryRevision == 0 || maximumReferences <= 0 || maximumCandidatesPerMention <= 0 ||
		len(recordID) == 0 || len(recordID) > 256 {
		return nil, errors.New("candidate batch requires source, producer, identity, revision, and positive limits")
	}
	for i := 0; i < len(recordID); i++ {
		if recordID[i] < 33 || recordID[i] > 126 {
			return nil, errors.New("candidate batch record ID is not an ASCII ID")
		}
	}
	remainingBytes := DefaultWireLimits.MaxBytes
	consumeBytes := func(message proto.Message) error {
		if message == nil {
			return errors.New("candidate batch input exceeds wire byte budget")
		}
		size := proto.Size(message)
		if size > remainingBytes {
			return errors.New("candidate batch input exceeds wire byte budget")
		}
		remainingBytes -= size
		return nil
	}
	for _, message := range []proto.Message{source.Meta, source.Context, sourceRef, producer} {
		if err := consumeBytes(message); err != nil {
			return nil, err
		}
	}
	work := 1 // Source artifact dependency.
	add := func(n int) error {
		if n < 0 || n > maximumReferences-work {
			return errors.New("candidate batch exceeds reference budget")
		}
		work += n
		return nil
	}
	if err := add(len(lookups)); err != nil {
		return nil, err
	}
	if err := add(len(candidates)); err != nil {
		return nil, err
	}
	if err := add(len(aliases)); err != nil {
		return nil, err
	}
	revisions := make(map[string]*pb.LookupScopeRevision)
	for _, lookup := range lookups {
		if lookup == nil || len(lookup.Scopes) == 0 {
			return nil, errors.New("candidate lookup is nil or has no scope")
		}
		if err := consumeBytes(lookup); err != nil {
			return nil, err
		}
		if err := add(len(lookup.Scopes)); err != nil {
			return nil, err
		}
		for _, scope := range lookup.Scopes {
			if scope == nil || scope.Revision == nil || scope.Revision.ScopeId == "" {
				return nil, errors.New("candidate lookup has no revision")
			}
			if err := add(len(scope.CandidateIds)); err != nil {
				return nil, err
			}
			if previous, exists := revisions[scope.Revision.ScopeId]; exists {
				if !proto.Equal(previous, scope.Revision) {
					return nil, fmt.Errorf("lookup scope %q has inconsistent revision", scope.Revision.ScopeId)
				}
			} else {
				revisions[scope.Revision.ScopeId] = proto.Clone(scope.Revision).(*pb.LookupScopeRevision)
			}
		}
	}
	if err := add(len(revisions)); err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if candidate == nil {
			return nil, errors.New("candidate entity is nil")
		}
		for _, key := range candidate.IdentityKeys {
			if key == nil {
				return nil, errors.New("candidate identity key is nil")
			}
		}
		if err := consumeBytes(candidate); err != nil {
			return nil, err
		}
		if err := add(len(candidate.IdentityKeys)); err != nil {
			return nil, err
		}
	}
	for _, alias := range aliases {
		if alias == nil {
			return nil, errors.New("candidate alias is nil")
		}
		if err := consumeBytes(alias); err != nil {
			return nil, err
		}
		if err := add(len(alias.SupportRefs)); err != nil {
			return nil, err
		}
	}
	lookupCopies := make([]*pb.CandidateLookup, 0, len(lookups))
	for _, lookup := range lookups {
		copy := proto.Clone(lookup).(*pb.CandidateLookup)
		for _, scope := range copy.Scopes {
			sort.Strings(scope.CandidateIds)
		}
		sort.Slice(copy.Scopes, func(i, j int) bool {
			return copy.Scopes[i].Revision.ScopeId < copy.Scopes[j].Revision.ScopeId
		})
		lookupCopies = append(lookupCopies, copy)
	}
	sort.Slice(lookupCopies, func(i, j int) bool { return lookupCopies[i].MentionId < lookupCopies[j].MentionId })
	entityCopies := make([]*pb.CanonicalEntity, 0, len(candidates))
	for _, candidate := range candidates {
		copy := proto.Clone(candidate).(*pb.CanonicalEntity)
		sort.Slice(copy.IdentityKeys, func(i, j int) bool {
			if copy.IdentityKeys[i].Namespace != copy.IdentityKeys[j].Namespace {
				return copy.IdentityKeys[i].Namespace < copy.IdentityKeys[j].Namespace
			}
			return copy.IdentityKeys[i].Value < copy.IdentityKeys[j].Value
		})
		entityCopies = append(entityCopies, copy)
	}
	sort.Slice(entityCopies, func(i, j int) bool {
		return entityCopies[i].GetMeta().GetRecordId() < entityCopies[j].GetMeta().GetRecordId()
	})
	aliasCopies := make([]*pb.Alias, 0, len(aliases))
	for _, alias := range aliases {
		copy := proto.Clone(alias).(*pb.Alias)
		sort.Strings(copy.SupportRefs)
		aliasCopies = append(aliasCopies, copy)
	}
	sort.Slice(aliasCopies, func(i, j int) bool {
		return aliasCopies[i].GetMeta().GetRecordId() < aliasCopies[j].GetMeta().GetRecordId()
	})
	revisionIDs := make([]string, 0, len(revisions))
	for id := range revisions {
		revisionIDs = append(revisionIDs, id)
	}
	sort.Strings(revisionIDs)
	revisionCopies := make([]*pb.LookupScopeRevision, 0, len(revisionIDs))
	for _, id := range revisionIDs {
		revisionCopies = append(revisionCopies, revisions[id])
	}
	manifestID := sha256.Sum256([]byte(recordID))
	batch := &pb.RegistryCandidateBatch{
		Meta: &pb.RecordMeta{SchemaVersion: source.Meta.SchemaVersion,
			CorpusId: source.Meta.CorpusId, RecordId: recordID},
		Context:               proto.Clone(source.Context).(*pb.RequestContext),
		SourceExtractionBatch: proto.Clone(sourceRef).(*pb.ArtifactRef),
		RegistryRevision:      registryRevision,
		Lookups:               lookupCopies,
		Candidates:            entityCopies,
		Aliases:               aliasCopies,
		Dependencies: &pb.DependencyManifest{
			ArtifactId:           fmt.Sprintf("dependencies:%x", manifestID),
			Dependencies:         []*pb.Dependency{{DependencyId: sourceRef.ArtifactId, Fingerprint: proto.Clone(sourceRef.ContentHash).(*pb.ContentHash)}},
			ProducerManifest:     proto.Clone(producer).(*pb.ProducerManifest),
			LookupScopeRevisions: revisionCopies,
		},
		Completeness: pb.Completeness_COMPLETENESS_COMPLETE,
	}
	if err := ValidateRegistryCandidateBatch(batch, source, sourceRef, maximumReferences, maximumCandidatesPerMention); err != nil {
		return nil, fmt.Errorf("candidate batch closure: %w", err)
	}
	if err := ValidateWire(batch, DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("candidate batch wire: %w", err)
	}
	return batch, nil
}
