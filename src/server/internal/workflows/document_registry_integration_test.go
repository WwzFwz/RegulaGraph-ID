// Verifies BIND identity reuse with the production binder/allocator and real
// PostgreSQL rows in an isolated schema. Unrelated additions preserve identity;
// a synthetic lifetime closure invalidates reuse at/after its revision. Explicit
// row edits are fault fixtures, not a production legal merge/split policy.
package workflows

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

func TestDocumentRegistryViewAgainstPostgres(t *testing.T) {
	repo, db := reviewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	files, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	executor, err := NewBindingExecutor(repo, files, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := bindingWorkflowBatch(true)
	rawInput, err := proto.MarshalOptions{Deterministic: true}.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	bound, observed, err := executor.bind(ctx, domain.JobRecord{JobID: "job:binding-history", CorpusID: input.Meta.CorpusId}, fmt.Sprintf("%x", sha256.Sum256(rawInput)), input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(bound)
	if err != nil {
		t.Fatal(err)
	}
	ref := bindingArtifactReference(raw)
	if err = repo.RegisterArtifact(ctx, input.Meta.CorpusId, ref); err != nil {
		t.Fatal(err)
	}
	if err = repo.VerifyDocumentRegistryView(ctx, input.Meta.CorpusId, ref, raw, observed, 100); err != nil {
		t.Fatal("original BIND registry view", err)
	}
	claim := domain.CanonicalIdentityClaim{ProposalKey: "unrelated", EntityType: domain.CanonicalEntityTypeOrganization,
		IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: bindingHash("a").Sha256, PayloadHash: bindingHash("b").Sha256}
	_, next, err := repo.ResolveCanonicalIdentities(ctx, input.Meta.CorpusId, "unrelated-addition", observed, []domain.CanonicalIdentityClaim{claim})
	if err != nil {
		t.Fatal(err)
	}
	if next <= observed {
		t.Fatal("fixture did not advance revision")
	}
	if err = repo.VerifyDocumentRegistryView(ctx, input.Meta.CorpusId, ref, raw, next, 100); err != nil {
		t.Fatal("unrelated advance invalidated exact BIND identities", err)
	}
	bad := append([]byte(nil), raw...)
	bad[0] ^= 1
	if err = repo.VerifyDocumentRegistryView(ctx, input.Meta.CorpusId, ref, bad, next, 100); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("source bytes not authenticated: %v", err)
	}
	if _, err = db.Exec(ctx, `UPDATE canonical_identities SET valid_to_revision=$3 WHERE corpus_id=$1 AND canonical_id=$2`, input.Meta.CorpusId, bound.Regulations[0].IssuerId, int64(next)); err != nil {
		t.Fatal(err)
	}
	if err = repo.VerifyDocumentRegistryView(ctx, input.Meta.CorpusId, ref, raw, observed, 100); err != nil {
		t.Fatal("historical identity before closure lost", err)
	}
	if err = repo.VerifyDocumentRegistryView(ctx, input.Meta.CorpusId, ref, raw, next, 100); !errors.Is(err, domain.ErrResolutionReplan) {
		t.Fatalf("closed issuer admitted: %v", err)
	}
	if err = repo.VerifyDocumentRegistryView(ctx, input.Meta.CorpusId, ref, raw, next+1, 100); err == nil {
		t.Fatal("future registry target admitted")
	}
}
