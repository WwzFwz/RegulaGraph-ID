// Exercises canonical view export using real PostgreSQL identity/profile/publication
// rows. Explicit synthetic versions test historical selection and corruption refusal;
// they do not implement a public profile-edit policy or demonstrate legal quality.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestRegistryEntityViewAgainstPostgres(t *testing.T) {
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
	corpus := fmt.Sprintf("corpus:view:%d", time.Now().UnixNano())
	claim := domain.CanonicalIdentityClaim{ProposalKey: "issuer", EntityType: domain.CanonicalEntityTypeOrganization, IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("a", 64), PayloadHash: strings.Repeat("b", 64)}
	assignments, rev, err := r.ResolveCanonicalIdentities(ctx, corpus, "seed", 1, []domain.CanonicalIdentityClaim{claim})
	if err != nil {
		t.Fatal(err)
	}
	entity := &pb.CanonicalEntity{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: assignments[0].CanonicalID}, EntityType: "organization", Scope: "ID:national", PreferredLabel: "Old label", RegistryRevision: rev,
		IdentityKeys: []*pb.IdentityKey{{Namespace: claim.IdentityScope, Value: claim.IdentityKey}}, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}
	alias := &pb.Alias{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "alias:seed"}, CanonicalId: entity.Meta.RecordId, Surface: "Old label", NormalizedLookup: "old label", Language: "id", Scope: entity.Scope, SupportRefs: []string{"support:fixture"}}
	rev, err = r.RegisterCanonicalAliases(ctx, corpus, "alias", rev, []AliasRegistration{{Entity: entity, Alias: alias}})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := r.ReservePublication(ctx, "publication:"+corpus, "", corpus, "snapshot:"+corpus, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.BindPublicationRegistry(ctx, reservation.PublicationID, corpus, reservation.Fence, rev); err != nil {
		t.Fatal(err)
	}
	request := RegistryEntityExport{ViewID: "view:test", CorpusID: corpus, PublicationID: reservation.PublicationID, Fence: reservation.Fence, Revision: rev, EntityIDs: []string{entity.Meta.RecordId},
		MaximumEntities: 10, MaximumBytes: 1 << 20, Producer: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("c", 64)}}}
	first, err := r.ExportRegistryEntityView(ctx, request)
	if err != nil || len(first.GetEntities()) != 1 || first.Entities[0].RegistryRevision != rev || first.Entities[0].PreferredLabel != "Old label" {
		t.Fatalf("export=%v err=%v", first, err)
	}
	for name, mutate := range map[string]func(*RegistryEntityExport){
		"foreign corpus":  func(q *RegistryEntityExport) { q.CorpusID = "corpus:foreign" },
		"wrong fence":     func(q *RegistryEntityExport) { q.Fence++ },
		"wrong revision":  func(q *RegistryEntityExport) { q.Revision++ },
		"missing":         func(q *RegistryEntityExport) { q.EntityIDs = []string{"canonical:missing"} },
		"partial missing": func(q *RegistryEntityExport) { q.EntityIDs = []string{"canonical:missing", entity.Meta.RecordId} },
		"duplicate":       func(q *RegistryEntityExport) { q.EntityIDs = []string{entity.Meta.RecordId, entity.Meta.RecordId} },
		"bytes":           func(q *RegistryEntityExport) { q.MaximumBytes = 100 },
	} {
		t.Run(name, func(t *testing.T) {
			q := request
			mutate(&q)
			if _, e := r.ExportRegistryEntityView(ctx, q); e == nil {
				t.Fatal("invalid export accepted")
			}
		})
	}
	empty := request
	empty.EntityIDs = nil
	if v, e := r.ExportRegistryEntityView(ctx, empty); e != nil || len(v.GetEntities()) != 0 {
		t.Fatalf("empty explicit selection: %v", e)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence+1 WHERE corpus_id=$1`, corpus); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ExportRegistryEntityView(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publisher: %v", err)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence-1 WHERE corpus_id=$1`, corpus); err != nil {
		t.Fatal(err)
	}
	// Inject a future profile version to prove the reader applies the bound revision,
	// without claiming the current registration API supports edits/merge/split.
	changed := proto.Clone(entity).(*pb.CanonicalEntity)
	changed.RegistryRevision = 0
	changed.PreferredLabel = "New label"
	raw, digest, err := marshalRegistryRecord(changed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE registry_entity_profiles SET to_revision=$3 WHERE corpus_id=$1 AND canonical_id=$2`, corpus, entity.Meta.RecordId, int64(rev+1)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO registry_entity_profiles(corpus_id,canonical_id,from_revision,entity_type,canonical_scope,preferred_label,review_state,profile_payload,record_hash)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, corpus, entity.Meta.RecordId, int64(rev+1), changed.EntityType, changed.Scope, changed.PreferredLabel, int16(changed.ReviewState), raw, digest); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE corpus_state SET registry_revision=$2 WHERE corpus_id=$1`, corpus, int64(rev+1)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := r.ExportRegistryEntityView(ctx, request)
	if err != nil || !proto.Equal(first, old) {
		t.Fatalf("historical export changed: %v", err)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE registry_entity_profiles SET record_hash=$3 WHERE corpus_id=$1 AND canonical_id=$2 AND from_revision=$4`, corpus, entity.Meta.RecordId, strings.Repeat("0", 64), int64(rev)); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ExportRegistryEntityView(ctx, request); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("corrupt profile accepted: %v", err)
	}
}
