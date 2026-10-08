// Checks candidate-context reuse, including unselected records and negative lookups.
// Synthetic cases isolate context comparison; real PostgreSQL tests separately verify
// registered bytes and historical views. These tests do not measure model accuracy.
package postgres

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestRegistryCandidateViewRejectsChangedContext(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]RegistryLookupResult) []RegistryLookupResult
	}{
		{"scope revision", func(r []RegistryLookupResult) []RegistryLookupResult { r[0].Revision.Revision++; return r }},
		{"profile label", func(r []RegistryLookupResult) []RegistryLookupResult {
			r[0].Candidates[0].PreferredLabel = "Other"
			return r
		}},
		{"profile revision", func(r []RegistryLookupResult) []RegistryLookupResult { r[0].Candidates[0].RegistryRevision++; return r }},
		{"alias evidence", func(r []RegistryLookupResult) []RegistryLookupResult {
			r[0].Aliases[0].SupportRefs = []string{"support:other"}
			return r
		}},
		{"removed alias", func(r []RegistryLookupResult) []RegistryLookupResult { r[0].Aliases = nil; return r }},
		{"removed candidate", func(r []RegistryLookupResult) []RegistryLookupResult { r[0].Candidates = nil; return r }},
		{"missing scope", func(r []RegistryLookupResult) []RegistryLookupResult { return nil }},
		{"duplicated scope", func(r []RegistryLookupResult) []RegistryLookupResult { return append(r, r[0]) }},
		{"foreign scope", func(r []RegistryLookupResult) []RegistryLookupResult { r[0].Scope.CanonicalScope = "other"; return r }},
		{"negative became positive", func(r []RegistryLookupResult) []RegistryLookupResult {
			r[1].Candidates = r[0].Candidates
			r[1].Aliases = r[0].Aliases
			r[1].Revision.EmptyResult = false
			r[1].Revision.Revision = 8
			return r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, ref, producer, plans, scopes, results := candidateReadFixture()
			negative := RegistryLookupScope{EntityType: "organization", CanonicalScope: "national", NormalizedLookup: "absent"}
			plans[0].Scopes = append(plans[0].Scopes, negative)
			scopes = append(scopes, negative)
			results = append(results, RegistryLookupResult{Scope: negative, Revision: &pb.LookupScopeRevision{
				ScopeId: domain.RegistryLookupScopeID(negative.EntityType, negative.CanonicalScope, negative.NormalizedLookup), EmptyResult: true}})
			batch, err := assembleRegistryCandidateResults(source, ref, producer, "candidates:revalidate", plans, scopes, results, 7, 64, 8)
			if err != nil {
				t.Fatal(err)
			}
			original := proto.Clone(batch)
			if err = compareRegistryCandidateView(batch, results); err != nil {
				t.Fatal(err)
			}
			if err = compareRegistryCandidateView(batch, tc.change(results)); !errors.Is(err, domain.ErrResolutionReplan) {
				t.Fatalf("changed context was reusable: %v", err)
			}
			if !proto.Equal(batch, original) {
				t.Fatal("historical candidate artifact was mutated")
			}
		})
	}
}

func TestRegistryCandidateViewComparesUnselectedProfilesAndIgnoresOrder(t *testing.T) {
	source, ref, producer, plans, scopes, results := candidateReadFixture()
	second := proto.Clone(results[0].Candidates[0]).(*pb.CanonicalEntity)
	second.Meta.RecordId = "canonical:two"
	alias := proto.Clone(results[0].Aliases[0]).(*pb.Alias)
	alias.Meta.RecordId, alias.CanonicalId = "alias:two", second.Meta.RecordId
	results[0].Candidates = append(results[0].Candidates, second)
	results[0].Aliases = append(results[0].Aliases, alias)
	batch, err := assembleRegistryCandidateResults(source, ref, producer, "candidates:ambiguous", plans, scopes, results, 7, 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	results[0].Candidates[0], results[0].Candidates[1] = results[0].Candidates[1], results[0].Candidates[0]
	results[0].Aliases[0], results[0].Aliases[1] = results[0].Aliases[1], results[0].Aliases[0]
	if err = compareRegistryCandidateView(batch, results); err != nil {
		t.Fatal(err)
	}
	second.PreferredLabel = "Changed competing entity"
	if err = compareRegistryCandidateView(batch, results); !errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("changed competing candidate was ignored: %v", err)
	}
}
