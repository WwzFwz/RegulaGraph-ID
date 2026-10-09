// Exercises durable registry history against real PostgreSQL: new unpublished
// aliases cannot leak into old snapshot pins, publication swaps retain old reads,
// replay is immutable, and foreign/expired pins and unsupported history fail.
// Synthetic aliases prove storage consistency, not legal correctness or latency.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestRegistrySnapshotHistoryAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dir, _ := filepath.Abs("../../../../../migrations")
	if err = r.ApplyMigrations(ctx, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	corpus := fmt.Sprintf("corpus:history:%d", time.Now().UnixNano())
	claim := domain.CanonicalIdentityClaim{ProposalKey: "issuer:history", EntityType: domain.CanonicalEntityTypeOrganization,
		IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("a", 64), PayloadHash: strings.Repeat("b", 64)}
	assignments, rev, err := r.ResolveCanonicalIdentities(ctx, corpus, "seed", 1, []domain.CanonicalIdentityClaim{claim})
	if err != nil {
		t.Fatal(err)
	}
	entity := &pb.CanonicalEntity{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: assignments[0].CanonicalID},
		EntityType: "organization", Scope: "ID:national", PreferredLabel: "Kementerian X", RegistryRevision: rev,
		ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED, IdentityKeys: []*pb.IdentityKey{{Namespace: claim.IdentityScope, Value: claim.IdentityKey}}}
	register := func(id, normalized string, expected uint64) uint64 {
		t.Helper()
		alias := &pb.Alias{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: id}, CanonicalId: entity.Meta.RecordId,
			Surface: normalized, NormalizedLookup: normalized, Language: "id", Scope: entity.Scope, SupportRefs: []string{"support:" + id}}
		next, e := r.RegisterCanonicalAliases(ctx, corpus, "op:"+id, expected, []AliasRegistration{{Entity: entity, Alias: alias}})
		if e != nil {
			t.Fatal(e)
		}
		return next
	}
	rev = register("alias:first", "old name", rev)
	scopes := []RegistryLookupScope{{EntityType: "organization", CanonicalScope: entity.Scope, NormalizedLookup: "old name"},
		{EntityType: "organization", CanonicalScope: entity.Scope, NormalizedLookup: "new name"}}
	pub, snap := "publication:old:"+corpus, "snapshot:old:"+corpus
	reservation, err := r.ReservePublication(ctx, pub, "", corpus, snap, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		corpus          string
		fence, revision uint64
	}{
		{"corpus:foreign", reservation.Fence, rev}, {corpus, reservation.Fence + 1, rev}, {corpus, reservation.Fence, rev - 1},
	} {
		if e := r.BindPublicationRegistry(ctx, pub, attempt.corpus, attempt.fence, attempt.revision); !errors.Is(e, ErrConflict) {
			t.Fatalf("invalid binding accepted: %v", e)
		}
	}
	if err = r.BindPublicationRegistry(ctx, pub, corpus, reservation.Fence, rev); err != nil {
		t.Fatal(err)
	}
	manifest := publicationFixture(corpus, pub, snap, reservation.Sequence, reservation.Fence, nil)
	publish := func(m *pb.PublicationManifest) {
		t.Helper()
		if e := r.StagePublication(ctx, m); e != nil {
			t.Fatal(e)
		}
		if e := r.RecordBackendReceipt(ctx, receiptFixture(m.Meta.RecordId, m.Fence)); e != nil {
			t.Fatal(e)
		}
		if e := r.CommitPublication(ctx, m.Meta.RecordId); e != nil {
			t.Fatal(e)
		}
	}
	publish(manifest)
	pin, err := r.PinActiveSnapshot(ctx, corpus, "lease:old:"+corpus, "reader:history", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID)
	next := register("alias:second", "new name", rev)
	next = register("alias:third", "old name", next)
	checkOld := func(repo *Repository) {
		t.Helper()
		got, observed, e := repo.LookupPinnedCanonicalAliases(ctx, pin, scopes, 2, 2)
		if e != nil || observed != rev || len(got) != 2 || len(got[0].Aliases) != 1 || !got[1].Revision.EmptyResult || got[1].Revision.Revision != 0 {
			t.Fatalf("old snapshot changed: %+v rev=%d err=%v", got, observed, e)
		}
	}
	checkOld(r)
	if err = r.BindPublicationRegistry(ctx, pub, corpus, reservation.Fence, rev); err != nil {
		t.Fatalf("historical replay: %v", err)
	}
	if err = r.BindPublicationRegistry(ctx, pub, corpus, reservation.Fence, next); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	latest, observed, err := r.LookupCanonicalAliases(ctx, corpus, scopes, 2, 2)
	if err != nil || observed != next || latest[1].Revision.EmptyResult {
		t.Fatalf("latest registry missing new alias: %v", err)
	}
	second, err := r.ReservePublication(ctx, "publication:new:"+corpus, "", corpus, "snapshot:new:"+corpus, snap)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.BindPublicationRegistry(ctx, second.PublicationID, corpus, second.Fence, next); err != nil {
		t.Fatal(err)
	}
	publish(publicationFixture(corpus, second.PublicationID, second.SnapshotID, second.Sequence, second.Fence, manifest.SnapshotRef))
	checkOld(r)
	newPin, err := r.PinActiveSnapshot(ctx, corpus, "lease:new:"+corpus, "reader:history", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.ReleaseSnapshotPin(ctx, newPin.LeaseID, newPin.OwnerID)
	got, observed, err := r.LookupPinnedCanonicalAliases(ctx, newPin, scopes, 2, 2)
	if err != nil || observed != next || got[1].Revision.EmptyResult || len(got[0].Aliases) != 2 {
		t.Fatalf("new snapshot stale: %v", err)
	}
	if _, _, err = r.LookupPinnedCanonicalAliases(ctx, newPin, scopes, 2, 1); !errors.Is(err, ErrResultLimit) {
		t.Fatalf("historical count cap bypassed: %v", err)
	}
	t.Run("release during blocked lookup", func(t *testing.T) {
		checkRegistryLookupRelease(t, ctx, r, corpus, scopes)
	})
	if _, err = r.AdvanceLookupScope(ctx, corpus, domain.RegistryLookupScopeID("organization", entity.Scope, "old name"), next); !errors.Is(err, ErrConflict) {
		t.Fatalf("generic scope writer changed alias history: %v", err)
	}
	missingScope := domain.RegistryLookupScopeID("organization", entity.Scope, "never observed alias")
	if _, err = r.AdvanceLookupScope(ctx, corpus, missingScope, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("generic scope writer created unstamped negative registry scope: %v", err)
	}
	var injectedScopes int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM lookup_scope_revisions WHERE corpus_id=$1 AND scope_key=$2`, corpus, missingScope).Scan(&injectedScopes); err != nil || injectedScopes != 0 {
		t.Fatalf("rejected generic registry scope left rows: count=%d err=%v", injectedScopes, err)
	}
	// Reopen the repository to prove history does not depend on in-process caching.
	reopened, e := Open(ctx, Config{DSN: dsn, MaxConnections: 2, HealthTimeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	checkOld(reopened)
	reopened.Close()
	for _, bad := range []domain.SnapshotPin{
		{LeaseID: pin.LeaseID, OwnerID: "foreign", CorpusID: pin.CorpusID, SnapshotID: pin.SnapshotID, Sequence: pin.Sequence, ExpiresAt: pin.ExpiresAt},
		{LeaseID: pin.LeaseID, OwnerID: pin.OwnerID, CorpusID: pin.CorpusID, SnapshotID: pin.SnapshotID, Sequence: pin.Sequence + 1, ExpiresAt: pin.ExpiresAt},
		{LeaseID: pin.LeaseID, OwnerID: pin.OwnerID, CorpusID: pin.CorpusID, SnapshotID: pin.SnapshotID, Sequence: pin.Sequence, ExpiresAt: pin.ExpiresAt.Add(time.Second)},
	} {
		if _, _, e = r.LookupPinnedCanonicalAliases(ctx, bad, scopes, 2, 2); e == nil {
			t.Fatal("forged pin accepted")
		}
	}
	for _, badRevision := range []uint64{0, next + 1, ^uint64(0)} {
		if _, _, e = r.LookupCanonicalAliasesAtRevision(ctx, corpus, badRevision, scopes, 2, 2); e == nil {
			t.Fatal("invalid revision accepted")
		}
	}
	if _, e = r.pool.Exec(ctx, `UPDATE snapshot_registry_bindings SET registry_revision=registry_revision+1 WHERE publication_id=$1`, pub); e == nil {
		t.Fatal("binding mutable")
	}
	if _, e = r.pool.Exec(ctx, `DELETE FROM registry_lookup_history WHERE corpus_id=$1`, corpus); e == nil {
		t.Fatal("history mutable")
	}
	// Simulate a corpus upgraded at the newer revision: earlier history must fail.
	if _, e = r.pool.Exec(ctx, `UPDATE corpus_state SET registry_history_floor=$2 WHERE corpus_id=$1`, corpus, int64(next)); e != nil {
		t.Fatal(e)
	}
	if _, _, e = r.LookupCanonicalAliasesAtRevision(ctx, corpus, rev, scopes, 2, 2); !errors.Is(e, ErrConflict) {
		t.Fatalf("pre-migration history fabricated: %v", e)
	}
	if e = r.ReleaseSnapshotPin(ctx, newPin.LeaseID, newPin.OwnerID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = r.LookupPinnedCanonicalAliases(ctx, newPin, scopes, 2, 2); !errors.Is(e, domain.ErrLeaseUnavailable) {
		t.Fatalf("released lease accepted: %v", e)
	}
}

func checkRegistryLookupRelease(t *testing.T, ctx context.Context, r *Repository, corpus string, scopes []RegistryLookupScope) {
	t.Helper()
	pin, err := r.PinActiveSnapshot(ctx, corpus, "lease:race:"+corpus, "reader:history", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer r.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID)
	blocker, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `LOCK TABLE registry_alias_versions IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, e := r.LookupPinnedCanonicalAliases(ctx, pin, scopes, 2, 2)
		done <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='registry_alias_versions'::regclass AND NOT granted)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reader never reached alias lookup lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = r.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, domain.ErrLeaseUnavailable) {
			t.Fatalf("released in-flight lease accepted: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestRegistryHistoryMigrationAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := Open(ctx, Config{DSN: dsn, MaxConnections: 2, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("registry_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.pool.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
	cfg := admin.pool.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := &Repository{pool: pool}
	defer r.Close()
	dir, _ := filepath.Abs("../../../../../migrations")
	all := os.DirFS(dir)
	entries, err := fs.ReadDir(all, ".")
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") && entry.Name() < "0018" {
			raw, e := fs.ReadFile(all, entry.Name())
			if e != nil {
				t.Fatal(e)
			}
			before[entry.Name()] = &fstest.MapFile{Data: raw}
		}
	}
	if err = r.ApplyMigrations(ctx, before); err != nil {
		t.Fatal(err)
	}
	scopes := []RegistryLookupScope{{EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: "known"},
		{EntityType: "organization", CanonicalScope: "ID:national", NormalizedLookup: "legacy"}}
	if _, err = r.pool.Exec(ctx, `INSERT INTO corpus_state(corpus_id,registry_revision) VALUES('corpus:legacy',7)`); err != nil {
		t.Fatal(err)
	}
	for i, s := range scopes {
		var count any
		if i == 0 {
			count = int64(0)
		}
		if _, err = r.pool.Exec(ctx, `INSERT INTO lookup_scope_revisions(corpus_id,scope_key,revision,alias_result_count) VALUES('corpus:legacy',$1,4,$2)`,
			domain.RegistryLookupScopeID(s.EntityType, s.CanonicalScope, s.NormalizedLookup), count); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.ApplyMigrations(ctx, all); err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyMigrations(ctx, all); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	got, rev, err := r.LookupCanonicalAliasesAtRevision(ctx, "corpus:legacy", 7, scopes[:1], 1, 1)
	if err != nil || rev != 7 || got[0].Revision.Revision != 4 || !got[0].Revision.EmptyResult {
		t.Fatalf("baseline lost: %+v %d %v", got, rev, err)
	}
	if _, _, err = r.LookupCanonicalAliasesAtRevision(ctx, "corpus:legacy", 6, scopes[:1], 1, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("invented pre-upgrade history: %v", err)
	}
	if _, _, err = r.LookupCanonicalAliasesAtRevision(ctx, "corpus:legacy", 7, scopes[1:], 1, 1); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("unknown legacy count became empty: %v", err)
	}
}
