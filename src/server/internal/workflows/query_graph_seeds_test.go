// Tests graph seed policy ownership and callback admission before hydration.
// Fixtures exercise read-only identity/snapshot boundaries; native storage tests
// establish actual pinned lookup wiring, not semantic/model quality acceptance.
package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/query"
)

type querySeedStore struct {
	t     *testing.T
	calls int
}

func (s *querySeedStore) LookupPinnedCanonicalAliases(_ context.Context, p domain.SnapshotPin, keys []domain.RegistryLookupScope, _, _ int) ([]domain.RegistryLookupResult, uint64, error) {
	s.calls++
	rows := make([]domain.RegistryLookupResult, len(keys))
	for i, k := range keys {
		if k.CanonicalScope != "national" {
			s.t.Fatal("caller mutated frozen policy")
		}
		rows[i] = domain.RegistryLookupResult{Scope: k, Revision: &pb.LookupScopeRevision{ScopeId: domain.RegistryLookupScopeID(k.EntityType, k.CanonicalScope, k.NormalizedLookup), EmptyResult: true}}
	}
	return rows, 7, nil
}
func TestQueryGraphSeedPolicyOwnershipAndView(t *testing.T) {
	p := query.EntityLinkingPolicy{Namespaces: []query.EntityNamespace{{EntityType: "organization", Scope: "national"}}, MaximumQueryBytes: 4096, MaximumPhraseTokens: 4, MaximumPhrases: 128, MaximumLookups: 128, MaximumAliasesPerLookup: 16, MaximumSeeds: 64}
	s := &querySeedStore{t: t}
	resolve, err := NewQueryGraphSeedResolver(s, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Namespaces[0].Scope = "foreign"
	v := &domain.PinnedGraph{Pin: domain.SnapshotPin{CorpusID: "corpus:a", SnapshotID: "snapshot:a", Sequence: 2, ExpiresAt: time.Now().Add(time.Minute)}, Snapshot: &pb.SnapshotRef{CorpusId: "corpus:a", SnapshotId: "snapshot:a", Sequence: 2}, AuthScope: "scope:a", Catalog: domain.GraphCatalogBinding{Binding: domain.GraphBinding{CorpusID: "corpus:a", Sequence: 2, RegistryRevision: 7}}}
	if out, err := resolve(context.Background(), "tidak dikenal", v); err != nil || len(out.CanonicalIDs) != 0 {
		t.Fatal(out, err)
	}
	v.Catalog.Binding.Sequence++
	if _, err = resolve(context.Background(), "izin", v); err == nil || s.calls != 1 {
		t.Fatal("mismatched graph reached lookup", err, s.calls)
	}
}

func TestGraphBranchLinkReportAdmissionAndOwnership(t *testing.T) {
	for _, mode := range []string{"foreign", "oversized", "ownership"} {
		t.Run(mode, func(t *testing.T) {
			w, q, in, _, _ := graphRAGFixture(t)
			base := w.Search.Graph
			report := &query.EntityLinks{CorpusID: in.Context.CorpusId, SnapshotID: in.Context.SnapshotRef.SnapshotId, RegistryRevision: 7, CanonicalIDs: []string{"node:one"}}
			if mode == "foreign" {
				report.SnapshotID = "snapshot:foreign"
			}
			if mode == "oversized" {
				report.Matches = []query.QueryAliasMatch{{Surface: strings.Repeat("x", (4<<20)+1)}}
			}
			w.Search.Graph = func(c context.Context, i retrieval.SearchInput) (*retrieval.BranchOutput, error) {
				o, e := base(c, i)
				o.Linking = report
				return o, e
			}
			out, err := w.Search.SearchCandidates(context.Background(), in, q.RequestedProfile)
			if mode != "ownership" {
				if err == nil {
					t.Fatal("invalid callback report admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			report.CanonicalIDs[0] = "forged"
			for _, b := range out.Branches {
				if b.Linking != nil && b.Linking.CanonicalIDs[0] != "node:one" {
					t.Fatal("branch report not owned")
				}
			}
		})
	}
}
