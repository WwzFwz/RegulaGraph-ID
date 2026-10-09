// Adds explicit synthetic BIND metadata/identities to the historical Rust index
// fixture before its source bytes are registered. This exercises graph preparation
// against real registry rows without claiming that this fixture runs BIND/model
// inference. The production binder/allocator is tested separately in workflows.
package indexing

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func seedGraphDocumentRegistry(t *testing.T, ctx context.Context, db *pgx.Conn, source *pb.DocumentBatch) {
	t.Helper()
	if len(source.Regulations) != 1 || len(source.Sources) != 1 || len(source.Observations) != 1 || len(source.Editions) != 0 {
		t.Fatal("graph fixture requires one source/regulation/observation without editions")
	}
	var revision uint64
	if err := db.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, source.Meta.CorpusId).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	reg := source.Regulations[0]
	for _, field := range [][2]string{{"regulation_type", reg.Kind}, {"number", reg.OfficialNumber}, {"year", strconv.FormatUint(uint64(reg.Year), 10)}, {"issuer", "Penerbit Fixture"}, {"page_title", reg.Title}} {
		source.Observations[0].PortalMetadata = append(source.Observations[0].PortalMetadata, &pb.NamedValue{Name: field[0], Value: &pb.NamedValue_Text{Text: field[1]}})
	}
	metadataBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(source.Observations[0])
	if err != nil {
		t.Fatal(err)
	}
	metadataHash := sha256.Sum256(metadataBytes)
	source.Observations[0].MetadataHash = &pb.ContentHash{Sha256: fmt.Sprintf("%x", metadataHash)}
	source.Editions = []*pb.DocumentEdition{{
		Meta:         &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "edition:graph-fixture"},
		RegulationId: proto.String(reg.Meta.RecordId), SourceRefs: []string{source.Observations[0].Meta.RecordId},
		Language: "id", DocumentKind: pb.DocumentKind_DOCUMENT_KIND_REGULATION,
	}}
	source.DependencyManifest.LookupScopeRevisions = []*pb.LookupScopeRevision{{ScopeId: "canonical-registry", Revision: revision}}
	digest := sha256.Sum256([]byte(strings.Join([]string{"canonical-registry", "organization", reg.IssuerId, strconv.FormatUint(revision, 10)}, "\x00")))
	issuer := &pb.Dependency{DependencyId: reg.IssuerId, Fingerprint: &pb.ContentHash{Sha256: fmt.Sprintf("%x", digest)}}
	// Existing source fixtures may already declare the issuer as an external
	// dependency. Replace that fingerprint rather than duplicate its identity.
	found := false
	for i, dependency := range source.DependencyManifest.Dependencies {
		if dependency.DependencyId == reg.IssuerId {
			source.DependencyManifest.Dependencies[i], found = issuer, true
		}
	}
	if !found {
		source.DependencyManifest.Dependencies = append(source.DependencyManifest.Dependencies, issuer)
	}
	plan, err := domain.PlanDocumentRegistryDependencies(source, 1000)
	if err != nil {
		t.Fatal("graph document fixture", err)
	}
	for _, identity := range plan.Identities {
		if _, err = db.Exec(ctx, `INSERT INTO canonical_identities(corpus_id,canonical_id,entity_type,identity_scope,identity_key,valid_from_revision) VALUES($1,$2,$3,$4,$5,$6)`, source.Meta.CorpusId, identity.CanonicalID, identity.EntityType, identity.IdentityScope, identity.IdentityKey, int64(revision)); err != nil {
			t.Fatal(err)
		}
	}
}
