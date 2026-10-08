// Extends the real semantic registry fixture with historical candidate reuse checks.
// Registered protobuf bytes and immutable history prove consistency across unrelated
// writes and negative-to-positive lookup changes, not semantic accuracy or benchmarks.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func checkCandidateViewRevisionReuse(t *testing.T, ctx context.Context, repo *Repository, corpus string,
	source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef, sourceBytes []byte,
	producer *pb.ProducerManifest, plans []RegistryCandidatePlan, entity *pb.CanonicalEntity) {
	t.Helper()
	batch, err := repo.PrepareRegistryCandidateBatch(ctx, source, sourceRef, producer,
		"candidates:revalidation", plans, 8, 8, 128, 8)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	ref := &pb.ArtifactRef{ArtifactId: "artifact:candidates-revalidation", StorageKey: "objects/candidates-revalidation",
		ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])}, MediaType: "application/x-protobuf",
		SchemaVersion: 1, ByteSize: uint64(len(raw))}
	if err = repo.RegisterArtifact(ctx, corpus, ref); err != nil {
		t.Fatal(err)
	}
	input := domain.SemanticRegistryInputs{SourceRef: sourceRef, SourceBytes: sourceBytes, CandidateRef: ref, CandidateBytes: raw}
	check := func(revision uint64) error {
		return repo.VerifyRegistryCandidateView(ctx, corpus, input, revision, 8, 8, 128, 8)
	}
	if err = check(batch.RegistryRevision); err != nil {
		t.Fatal(err)
	}
	_, next, err := repo.ResolveCanonicalIdentities(ctx, corpus, "identity:unrelated-view", batch.RegistryRevision,
		[]domain.CanonicalIdentityClaim{{ProposalKey: "issuer:unrelated", EntityType: domain.CanonicalEntityTypeOrganization,
			IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("9", 64), PayloadHash: strings.Repeat("8", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	if err = check(next); err != nil {
		t.Fatalf("unrelated registry advance invalidated context: %v", err)
	}
	alias := &pb.Alias{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "alias:new-negative-context"},
		CanonicalId: entity.Meta.RecordId, Surface: "Unknown", NormalizedLookup: "unknown", Scope: entity.Scope,
		Language: "id", SupportRefs: []string{"support:new-negative-context"}}
	newRevision, err := repo.RegisterCanonicalAliases(ctx, corpus, "alias:new-negative-context", next,
		[]AliasRegistration{{Entity: entity, Alias: alias}})
	if err != nil {
		t.Fatal(err)
	}
	if err = check(newRevision); !errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("new positive context reused negative decision: %v", err)
	}
	if err = check(next); err != nil {
		t.Fatalf("retained context changed after alias write: %v", err)
	}
}
