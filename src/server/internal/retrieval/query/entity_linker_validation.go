// Validates complete alias lookup observations before query seed discovery. Exact
// key/revision, candidate/alias closure and aggregate bytes are checked; malformed
// or omitted alternatives fail rather than selecting the first plausible match.
// This read boundary does not establish legal truth or model confidence. Measure
// candidate recall, ambiguity, bytes and p95/p99 under benchmark-targets.yaml.
package query

import (
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func validateQueryAliasRows(rows []domain.RegistryLookupResult, keys []domain.RegistryLookupScope, corpus string, revision uint64, maximum int) (map[domain.RegistryLookupScope][]*pb.Alias, map[string]bool, error) {
	out := map[domain.RegistryLookupScope][]*pb.Alias{}
	reasons := map[string]bool{}
	remaining := 16 << 20
	charge := func(v proto.Message) bool {
		size := proto.Size(v)
		if size > remaining {
			return false
		}
		remaining -= size
		return domain.ValidateWire(v, domain.DefaultWireLimits) == nil
	}
	globalEntities := map[string]*pb.CanonicalEntity{}
	globalAliases := map[string]*pb.Alias{}
	for i, row := range rows {
		if row.Scope != keys[i] || row.Revision == nil || !charge(row.Revision) || row.Revision.ScopeId != domain.RegistryLookupScopeID(row.Scope.EntityType, row.Scope.CanonicalScope, row.Scope.NormalizedLookup) || row.Revision.Revision > revision || row.Revision.EmptyResult != (len(row.Candidates) == 0) || (row.Revision.Revision == 0 && !row.Revision.EmptyResult) || len(row.Candidates) > maximum || len(row.Aliases) > maximum {
			return nil, nil, errors.New("invalid query alias lookup observation")
		}
		entities := map[string]*pb.CanonicalEntity{}
		for _, e := range row.Candidates {
			if e == nil || !charge(e) || e.Meta.SchemaVersion != 1 || e.Meta.CorpusId != corpus || e.EntityType != row.Scope.EntityType || e.Scope != row.Scope.CanonicalScope || e.RegistryRevision == 0 || e.RegistryRevision > revision || (e.ReviewState != pb.ReviewState_REVIEW_STATE_APPROVED && e.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED) || entities[e.Meta.RecordId] != nil {
				return nil, nil, errors.New("invalid query canonical candidate")
			}
			if previous := globalEntities[e.Meta.RecordId]; previous != nil && !proto.Equal(previous, e) {
				return nil, nil, errors.New("conflicting query canonical candidate")
			}
			entities[e.Meta.RecordId] = e
			globalEntities[e.Meta.RecordId] = e
			if e.ReviewState == pb.ReviewState_REVIEW_STATE_UNREVIEWED {
				reasons["query_alias_unreviewed"] = true
			}
		}
		seen := map[string]bool{}
		covered := map[string]bool{}
		for _, a := range row.Aliases {
			if a == nil || !charge(a) || a.Meta.SchemaVersion != 1 || a.Meta.CorpusId != corpus || a.Scope != row.Scope.CanonicalScope || a.NormalizedLookup != row.Scope.NormalizedLookup || len(a.SupportRefs) == 0 || entities[a.CanonicalId] == nil || seen[a.Meta.RecordId] {
				return nil, nil, errors.New("invalid query alias support")
			}
			if previous := globalAliases[a.Meta.RecordId]; previous != nil && !proto.Equal(previous, a) {
				return nil, nil, errors.New("conflicting query alias")
			}
			globalAliases[a.Meta.RecordId] = a
			seen[a.Meta.RecordId] = true
			covered[a.CanonicalId] = true
			if a.ValidInterval != nil {
				reasons["query_alias_temporal_unresolved"] = true
			}
		}
		if len(covered) != len(entities) {
			return nil, nil, errors.New("query candidates lack aliases")
		}
		out[row.Scope] = row.Aliases
	}
	return out, reasons, nil
}
