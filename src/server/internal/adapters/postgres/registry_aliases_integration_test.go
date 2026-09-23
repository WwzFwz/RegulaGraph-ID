// Exercises alias registration and batched candidate lookup on a disposable real PostgreSQL
// cluster. The test covers ambiguous aliases, negative dependency revisions, operation replay,
// stale revision rejection, and bounded results; it is skipped without an explicit test DSN.
// This is correctness evidence, not model quality or a latency/throughput benchmark.
package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestRegistryAliasesAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("REGULAGRAPH_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `TRUNCATE TABLE corpus_state CASCADE`); err != nil {
		t.Fatal(err)
	}
	corpusID := "corpus-alias-test"
	claims := []domain.CanonicalIdentityClaim{
		{ProposalKey: "issuer:a", EntityType: domain.CanonicalEntityTypeOrganization,
			IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("a", 64),
			PayloadHash: strings.Repeat("1", 64)},
		{ProposalKey: "issuer:b", EntityType: domain.CanonicalEntityTypeOrganization,
			IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("b", 64),
			PayloadHash: strings.Repeat("2", 64)},
	}
	assignments, revision, err := repo.ResolveCanonicalIdentities(ctx, corpusID, "identity:seed", 1, claims)
	if err != nil || revision != 2 || len(assignments) != 2 {
		t.Fatalf("seed canonical identities: assignments=%v revision=%d err=%v", assignments, revision, err)
	}
	registration := func(index int, aliasID, normalized string) AliasRegistration {
		return AliasRegistration{
			Entity: &pb.CanonicalEntity{
				Meta:       &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: assignments[index].CanonicalID},
				EntityType: "organization", Scope: "ID:national", PreferredLabel: "Kementerian X",
				ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED, RegistryRevision: revision,
				IdentityKeys: []*pb.IdentityKey{
					{Namespace: claims[index].IdentityScope, Value: claims[index].IdentityKey},
					{Namespace: "external-reference", Value: "issuer-" + string(rune('a'+index))},
				},
			},
			Alias: &pb.Alias{
				Meta:        &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: aliasID},
				CanonicalId: assignments[index].CanonicalID, Surface: "Kementerian X",
				NormalizedLookup: normalized, Language: "id", Scope: "ID:national",
				SupportRefs: []string{"support:source-" + aliasID},
			},
		}
	}
	lookup := RegistryLookupScope{EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: "kementerian x"}
	missing := RegistryLookupScope{EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: "nama baru"}
	initial, observedRevision, err := repo.LookupCanonicalAliases(ctx, corpusID, []RegistryLookupScope{lookup, missing}, 4, 4)
	if err != nil || observedRevision != 2 || len(initial) != 2 || !initial[0].Revision.EmptyResult ||
		!initial[1].Revision.EmptyResult || initial[0].Revision.Revision != 0 {
		t.Fatalf("initial empty lookup: result=%v revision=%d err=%v", initial, observedRevision, err)
	}
	items := []AliasRegistration{registration(0, "alias:a", "kementerian x"), registration(1, "alias:b", "kementerian x")}
	registered, err := repo.RegisterCanonicalAliases(ctx, corpusID, "alias:seed", revision, items)
	if err != nil || registered != 3 {
		t.Fatalf("register ambiguous aliases: revision=%d err=%v", registered, err)
	}
	replayed, err := repo.RegisterCanonicalAliases(ctx, corpusID, "alias:seed", revision, items)
	if err != nil || replayed != registered {
		t.Fatalf("idempotent alias replay: revision=%d err=%v", replayed, err)
	}
	noOp, err := repo.RegisterCanonicalAliases(ctx, corpusID, "alias:repeat-existing", registered, items)
	if err != nil || noOp != registered {
		t.Fatalf("identical aliases advanced registry: revision=%d err=%v", noOp, err)
	}
	changed := []AliasRegistration{registration(0, "alias:a", "wrong"), items[1]}
	if _, err = repo.RegisterCanonicalAliases(ctx, corpusID, "alias:seed", revision, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	results, observedRevision, err := repo.LookupCanonicalAliases(ctx, corpusID, []RegistryLookupScope{lookup, missing}, 4, 4)
	if err != nil || observedRevision != registered || len(results[0].Candidates) != 2 ||
		len(results[0].Aliases) != 2 || results[0].Revision.Revision != registered ||
		results[0].Revision.EmptyResult || !results[1].Revision.EmptyResult || results[1].Revision.Revision != 0 ||
		len(results[0].Candidates[0].IdentityKeys) != 2 {
		t.Fatalf("ambiguous/negative lookup: result=%v revision=%d err=%v", results, observedRevision, err)
	}
	if _, _, err = repo.LookupCanonicalAliases(ctx, corpusID, []RegistryLookupScope{lookup}, 4, 1); !errors.Is(err, ErrResultLimit) {
		t.Fatalf("lookup cap was not enforced: %v", err)
	}
	if _, err = repo.RegisterCanonicalAliases(ctx, corpusID, "alias:stale", revision,
		[]AliasRegistration{registration(0, "alias:c", "nama baru")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale registry write accepted: %v", err)
	}
	advanced, err := repo.RegisterCanonicalAliases(ctx, corpusID, "alias:new-name", registered,
		[]AliasRegistration{registration(0, "alias:c", "nama baru")})
	if err != nil || advanced != 4 {
		t.Fatalf("advance negative lookup: revision=%d err=%v", advanced, err)
	}
	updated, _, err := repo.LookupCanonicalAliases(ctx, corpusID, []RegistryLookupScope{missing}, 4, 4)
	if err != nil || updated[0].Revision.EmptyResult || updated[0].Revision.Revision != advanced ||
		len(updated[0].Candidates) != 1 {
		t.Fatalf("negative lookup was not invalidated: result=%v err=%v", updated, err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_alias_versions SET record_hash=$3
		WHERE corpus_id=$1 AND alias_id=$2`, corpusID, "alias:a", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RegisterCanonicalAliases(ctx, corpusID, "alias:seed", revision, items); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("corrupt alias replay passed: %v", err)
	}
	originalHash, err := hashRegistryRecord(items[0].Alias)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_alias_versions SET record_hash=$3
		WHERE corpus_id=$1 AND alias_id=$2`, corpusID, "alias:a", originalHash); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct{ name, column, changed, restore string }{
		{"surface", "surface", "Kementerian lain", "Kementerian X"},
		{"support", "support_refs", "{support:forged}", "{support:source-alias:a}"},
		{"canonical owner", "canonical_id", assignments[1].CanonicalID, assignments[0].CanonicalID},
		{"lookup key", "normalized_lookup", "nama palsu", "kementerian x"},
	} {
		t.Run("tampered "+testCase.name, func(t *testing.T) {
			_, updateErr := repo.pool.Exec(ctx, `UPDATE registry_alias_versions SET `+testCase.column+`=$3
				WHERE corpus_id=$1 AND alias_id=$2`, corpusID, "alias:a", testCase.changed)
			if updateErr != nil {
				t.Fatal(updateErr)
			}
			if _, _, lookupErr := repo.LookupCanonicalAliases(ctx, corpusID, []RegistryLookupScope{lookup}, 4, 4); !errors.Is(lookupErr, domain.ErrPersistentIntegrity) {
				t.Fatalf("tampered alias was returned: %v", lookupErr)
			}
			if _, replayErr := repo.RegisterCanonicalAliases(ctx, corpusID, "alias:seed", revision, items); !errors.Is(replayErr, domain.ErrPersistentIntegrity) {
				t.Fatalf("tampered alias replay passed: %v", replayErr)
			}
			if _, reuseErr := repo.RegisterCanonicalAliases(ctx, corpusID, "alias:reuse-corrupt", advanced,
				[]AliasRegistration{items[0]}); !errors.Is(reuseErr, domain.ErrPersistentIntegrity) {
				t.Fatalf("tampered alias reuse passed: %v", reuseErr)
			}
			if _, updateErr = repo.pool.Exec(ctx, `UPDATE registry_alias_versions SET `+testCase.column+`=$3
				WHERE corpus_id=$1 AND alias_id=$2`, corpusID, "alias:a", testCase.restore); updateErr != nil {
				t.Fatal(updateErr)
			}
		})
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_entity_profiles SET preferred_label=$3
		WHERE corpus_id=$1 AND canonical_id=$2`, corpusID, assignments[0].CanonicalID, "Wrong label"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.LookupCanonicalAliases(ctx, corpusID, []RegistryLookupScope{lookup}, 4, 4); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("tampered profile was returned: %v", err)
	}
	if _, err = repo.RegisterCanonicalAliases(ctx, corpusID, "alias:reuse-profile", advanced,
		[]AliasRegistration{items[0]}); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("tampered profile was reused: %v", err)
	}
	if _, err = repo.pool.Exec(ctx, `UPDATE registry_entity_profiles SET preferred_label=$3
		WHERE corpus_id=$1 AND canonical_id=$2`, corpusID, assignments[0].CanonicalID, "Kementerian X"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.pool.Exec(ctx, `DELETE FROM registry_entity_profiles
		WHERE corpus_id=$1 AND canonical_id=$2`, corpusID, assignments[0].CanonicalID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RegisterCanonicalAliases(ctx, corpusID, "alias:orphan", advanced,
		[]AliasRegistration{items[0]}); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("orphan alias gained an unversioned profile: %v", err)
	}
}
