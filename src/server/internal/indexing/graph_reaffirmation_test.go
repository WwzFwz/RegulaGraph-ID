// Exercises retained RESOLVE reuse after an unrelated real registry allocation,
// then retries at a frozen target after another allocation. Native ASSEMBLE and
// publication consume the derived revision view; original model decisions stay
// historical. Synthetic extraction/review/vector data is not quality acceptance.
package indexing

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

func TestNativeGraphCrossRevisionReaffirmation(t *testing.T) {
	if os.Getenv("REGULAGRAPH_TEST_PREPARE_GRAPH_CLI") == "" {
		t.Skip("actual prepare-graph executable required")
	}
	t.Setenv("REGULAGRAPH_TEST_GRAPH_REAFFIRM", "1")
	TestNativeGraphAssemblyPipeline(t)
}

func advanceUnrelatedGraphRegistry(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, corpus, suffix string) uint64 {
	t.Helper()
	var revision uint64
	if err := db.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, corpus).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	_, next, err := repo.ResolveCanonicalIdentities(ctx, corpus, "identity:reaffirm:"+suffix, revision, []domain.CanonicalIdentityClaim{{ProposalKey: "issuer:unrelated:" + suffix,
		EntityType: domain.CanonicalEntityTypeOrganization, IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat(suffix, 64), PayloadHash: strings.Repeat("e", 64)}})
	if err != nil || next <= revision {
		t.Fatal("unrelated registry allocation", err)
	}
	return next
}

func checkReaffirmedSource(t *testing.T, ctx context.Context, files *storage.FileStore, b domain.GraphSourceBinding) {
	t.Helper()
	original, bound := new(pb.ResolutionBatch), new(pb.ResolutionBatch)
	for _, pair := range []struct {
		ref     *pb.ArtifactRef
		message *pb.ResolutionBatch
	}{{b.OriginalResolution, original}, {b.BoundResolution, bound}} {
		raw, err := files.ReadVerified(ctx, pair.ref, uint64(domain.DefaultWireLimits.MaxBytes))
		if err != nil {
			t.Fatal(err)
		}
		if err = domain.DecodeWire(raw, pair.message, domain.DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
	}
	if b.Policy != domain.GraphSourceReaffirmationPolicy || bound.RegistryRevision != b.RegistryRevision || original.RegistryRevision >= bound.RegistryRevision {
		t.Fatal("missing durable reaffirmation binding")
	}
	if len(original.Decisions) != len(bound.Decisions) || !proto.Equal(original.ModelManifest, bound.ModelManifest) {
		t.Fatal("resolution history changed")
	}
	for i, d := range original.Decisions {
		if !proto.Equal(d, bound.Decisions[i]) || d.RegistryRevision >= bound.RegistryRevision {
			t.Fatal("decision rewritten to target revision")
		}
	}
	t.Logf("reaffirmation: historical RESOLVE revision=%d, derived assembly revision=%d; decision bytes retained", original.RegistryRevision, bound.RegistryRevision)
}

func checkReaffirmationReceiptRace(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, pin domain.SnapshotPin, b domain.GraphSourceBinding, input domain.GraphJobSourceInputs) {
	t.Helper()
	source := input.Source
	source.Candidates = input.Candidates
	single, err := postgres.Open(ctx, postgres.Config{DSN: os.Getenv("REGULAGRAPH_TEST_QUERY_DSN"), MaxConnections: 1, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = single.RegisterGraphSourceBinding(ctx, pin, b, source, 4096); err != nil {
		single.Close()
		t.Fatal("single connection reaffirmation replay", err)
	}
	single.Close()
	bad := source
	bad.Candidates = append([]byte(nil), source.Candidates...)
	bad.Candidates[0] ^= 1
	if err = repo.RegisterGraphSourceBinding(ctx, pin, b, bad, 4096); err == nil {
		t.Fatal("corrupt historical candidate accepted for reaffirmation")
	}
	locked, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(context.Background())
	if _, err = locked.Exec(ctx, `SELECT publication_id FROM snapshots WHERE publication_id=$1 FOR UPDATE`, b.PublicationID); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- repo.RegisterGraphSourceBinding(bounded, pin, b, source, 4096) }()
	blocked := false
	deadline := time.Now().Add(4 * time.Second)
	for !blocked && time.Now().Before(deadline) {
		if err = locked.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND pg_backend_pid()=ANY(pg_blocking_pids(pid)))`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if !blocked {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("reaffirmation did not wait for publication lock")
	}
	// This write uses another connection; only the target snapshot is locked,
	// so the registry writer can advance after validation but before receipt CAS.
	var current uint64
	if err = locked.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, pin.CorpusID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.ResolveCanonicalIdentities(ctx, pin.CorpusID, "identity:reaffirm-race", current, []domain.CanonicalIdentityClaim{{ProposalKey: "issuer:race", EntityType: domain.CanonicalEntityTypeOrganization, IdentityScope: domain.IssuerIdentityKeyNamespace, IdentityKey: strings.Repeat("6", 64), PayloadHash: strings.Repeat("e", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	if err = locked.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("stale reaffirmation stamp accepted", err)
	}
	if err = repo.RegisterGraphSourceBinding(ctx, pin, b, source, 4096); err != nil {
		t.Fatal("fresh historical-view replay after unrelated advance", err)
	}
	t.Log("reaffirmation receipt: corrupt candidates rejected, single-connection replay and registry lock race verified")
}
