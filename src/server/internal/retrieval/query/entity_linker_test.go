// Exercises pinned query alias discovery against adversarial read responses and
// ambiguous legal namespaces. Tests cover byte offsets, alternatives, ownership,
// caps and cancellation; synthetic aliases do not establish gold linking accuracy.
package query

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type aliasReaderFunc func(context.Context, domain.SnapshotPin, []domain.RegistryLookupScope, int, int) ([]domain.RegistryLookupResult, uint64, error)

func (f aliasReaderFunc) LookupPinnedCanonicalAliases(c context.Context, p domain.SnapshotPin, s []domain.RegistryLookupScope, m, a int) ([]domain.RegistryLookupResult, uint64, error) {
	return f(c, p, s, m, a)
}

func aliasPolicy() EntityLinkingPolicy {
	return EntityLinkingPolicy{Namespaces: []EntityNamespace{{"organization", "national"}}, MaximumQueryBytes: 4096, MaximumPhraseTokens: 4, MaximumPhrases: 128, MaximumLookups: 256, MaximumAliasesPerLookup: 16, MaximumSeeds: 64}
}
func aliasPin() domain.SnapshotPin {
	return domain.SnapshotPin{CorpusID: "corpus:test", SnapshotID: "snapshot:test", Sequence: 7, ExpiresAt: time.Now().Add(time.Minute)}
}
func aliasRows(keys []domain.RegistryLookupScope) []domain.RegistryLookupResult {
	rows := make([]domain.RegistryLookupResult, len(keys))
	for i, k := range keys {
		r := domain.RegistryLookupResult{Scope: k, Revision: &pb.LookupScopeRevision{ScopeId: domain.RegistryLookupScopeID(k.EntityType, k.CanonicalScope, k.NormalizedLookup), EmptyResult: true}}
		if k.NormalizedLookup == "badan a" || k.NormalizedLookup == "pasal 1(2)" || k.NormalizedLookup == "izin" {
			id := "canonical:" + k.CanonicalScope
			meta := func(id string) *pb.RecordMeta {
				return &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:test", RecordId: id}
			}
			r.Revision.Revision = 3
			r.Revision.EmptyResult = false
			r.Candidates = []*pb.CanonicalEntity{{Meta: meta(id), EntityType: k.EntityType, Scope: k.CanonicalScope, PreferredLabel: "Badan A", RegistryRevision: 3, ReviewState: pb.ReviewState_REVIEW_STATE_APPROVED}}
			r.Aliases = []*pb.Alias{{Meta: meta("alias:" + k.CanonicalScope), CanonicalId: id, Scope: k.CanonicalScope, Surface: k.NormalizedLookup, NormalizedLookup: k.NormalizedLookup, Language: "id", SupportRefs: []string{"support:a"}}}
		}
		rows[i] = r
	}
	return rows
}

func TestQueryAliasLinkingAmbiguityAndOriginalOffsets(t *testing.T) {
	p := aliasPolicy()
	p.Namespaces = append(p.Namespaces, EntityNamespace{"organization", "regional"})
	text := "Siapa café ‘BADAN   A’?"
	calls := 0
	r := aliasReaderFunc(func(c context.Context, pin domain.SnapshotPin, keys []domain.RegistryLookupScope, m, a int) ([]domain.RegistryLookupResult, uint64, error) {
		calls++
		if _, ok := c.Deadline(); !ok {
			t.Fatal("no pin deadline")
		}
		return aliasRows(keys), 7, nil
	})
	out, err := LinkQueryAliases(context.Background(), r, aliasPin(), 7, text, p)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(out.CanonicalIDs) != 2 || !slices.Contains(out.StopReasons, "query_alias_ambiguous") || len(out.Matches) != 1 || len(out.Observations) < 2 {
		t.Fatal(out)
	}
	m := out.Matches[0]
	if text[m.StartByte:m.EndByte] != "BADAN   A" || m.Normalized != "badan a" || len(m.AliasIDs) != 2 {
		t.Fatal(m)
	}
	c := out.Clone()
	c.Matches[0].CanonicalIDs[0] = "mutated"
	c.Observations[0].Empty = !c.Observations[0].Empty
	if out.Matches[0].CanonicalIDs[0] == "mutated" || out.Observations[0].Empty == c.Observations[0].Empty {
		t.Fatal("clone shares report slices")
	}
}

func TestQueryAliasPhrasesPreserveNumbersAndUnknown(t *testing.T) {
	p := aliasPolicy()
	phrases, err := queryAliasPhrases("Apa isi Pasal 1(2)?", p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range phrases {
		found = found || p.Normalized == "pasal 1(2)"
	}
	if !found {
		t.Fatal("lost legal punctuation")
	}
	r := aliasReaderFunc(func(_ context.Context, _ domain.SnapshotPin, k []domain.RegistryLookupScope, _, _ int) ([]domain.RegistryLookupResult, uint64, error) {
		return aliasRows(k), 7, nil
	})
	out, err := LinkQueryAliases(context.Background(), r, aliasPin(), 7, "kata belum tersedia", p)
	if err != nil || len(out.CanonicalIDs) != 0 || !slices.Contains(out.StopReasons, "query_seed_unresolved") || len(out.Observations) == 0 {
		t.Fatal(out, err)
	}
}

func TestQueryAliasRejectsCorruptOrIncompleteReads(t *testing.T) {
	for name, mutate := range map[string]func([]domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64){
		"wrong revision": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) { return r, 8 },
		"missing key":    func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) { return nil, 7 },
		"wrong key": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Scope.CanonicalScope = "other"
			return r, 7
		},
		"wrong scope revision": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Revision.ScopeId = "lookup:other"
			return r, 7
		},
		"false negative": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Revision.EmptyResult = true
			return r, 7
		},
		"foreign corpus": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Candidates[0].Meta.CorpusId = "corpus:foreign"
			return r, 7
		},
		"future entity": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Candidates[0].RegistryRevision = 8
			return r, 7
		},
		"no support": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Aliases[0].SupportRefs = nil
			return r, 7
		},
		"orphan alias": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Aliases[0].CanonicalId = "canonical:unknown"
			return r, 7
		},
		"dropped alias": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Aliases = nil
			return r, 7
		},
		"duplicate candidate": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Candidates = append(r[0].Candidates, proto.Clone(r[0].Candidates[0]).(*pb.CanonicalEntity))
			return r, 7
		},
		"nil entity": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Candidates[0] = nil
			return r, 7
		},
		"nil alias": func(r []domain.RegistryLookupResult) ([]domain.RegistryLookupResult, uint64) {
			r[0].Aliases[0] = nil
			return r, 7
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := aliasReaderFunc(func(_ context.Context, _ domain.SnapshotPin, k []domain.RegistryLookupScope, _, _ int) ([]domain.RegistryLookupResult, uint64, error) {
				rows, rev := mutate(aliasRows(k))
				return rows, rev, nil
			})
			if _, err := LinkQueryAliases(context.Background(), r, aliasPin(), 7, "izin", aliasPolicy()); err == nil {
				t.Fatal("corrupt read accepted")
			}
		})
	}
}

func TestQueryAliasBudgetsAndCancellation(t *testing.T) {
	for _, name := range []string{"phrases", "lookups", "seeds", "query", "expired", "cancelled", "read failure"} {
		t.Run(name, func(t *testing.T) {
			p := aliasPolicy()
			pin := aliasPin()
			q := "badan a"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			switch name {
			case "phrases":
				p.MaximumPhrases = 1
			case "lookups":
				p.MaximumLookups = 1
			case "seeds":
				q = "izin"
				p.MaximumSeeds = 1
				p.Namespaces = append(p.Namespaces, EntityNamespace{"organization", "regional"})
			case "query":
				q = strings.Repeat("x", 4097)
			case "expired":
				pin.ExpiresAt = time.Now().Add(-time.Second)
			case "cancelled":
				cancel()
			}
			r := aliasReaderFunc(func(_ context.Context, _ domain.SnapshotPin, k []domain.RegistryLookupScope, _, _ int) ([]domain.RegistryLookupResult, uint64, error) {
				calls++
				if name == "read failure" {
					return nil, 0, errors.New("unavailable")
				}
				return aliasRows(k), 7, nil
			})
			if _, err := LinkQueryAliases(ctx, r, pin, 7, q, p); err == nil {
				t.Fatal("invalid request accepted")
			}
			if name != "seeds" && name != "read failure" && calls != 0 {
				t.Fatal("invalid input performed lookup")
			}
		})
	}
}
