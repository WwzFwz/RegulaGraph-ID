// Runs actual operator preparation before any graph reservation or derived
// artifact exists, then hands its stored inventory to the native Rust executor
// and optional publication CLI. Replays and rejected scope/cancellation must not
// leak pins or create partial jobs. Semantic labels/vectors remain fixtures.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkPrepareGraphCLI(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, files *storage.FileStore,
	source domain.IndexSourceBinding, artifacts indexMemoryArtifacts, ontology *domain.Ontology) {
	t.Helper()
	corpus, publication, target := source.Snapshot.CorpusId, "graph:"+source.PublicationID, "graph:"+source.Snapshot.SnapshotId
	checkGraphSourceSelectionFaults(t, ctx, repo, db, source)
	for _, ref := range []*pb.ArtifactRef{source.Original, source.Bound} {
		if _, err := files.Put(ctx, ref, bytes.NewReader(artifacts[ref.ArtifactId])); err != nil {
			t.Fatal(err)
		}
	}
	path, err := filepath.Abs("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "REGULAGRAPH_POSTGRES_DSN="+os.Getenv("REGULAGRAPH_TEST_QUERY_DSN"),
		"REGULAGRAPH_ARTIFACTS_DIR="+os.Getenv("REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT"), "REGULAGRAPH_ONTOLOGY_PATH="+path, "REGULAGRAPH_ONTOLOGY_SHA256="+fmt.Sprintf("%x", sha256.Sum256(raw)))
	args := []string{"prepare-graph", "-corpus", corpus, "-publication", publication, "-snapshot", target, "-base-snapshot", source.Snapshot.SnapshotId, "-auth-scope", source.AuthScope, "-timeout", "30s"}
	invoke := func(a []string, want bool) []string {
		t.Helper()
		cmd := exec.CommandContext(ctx, os.Getenv("REGULAGRAPH_TEST_PREPARE_GRAPH_CLI"), a...)
		cmd.Env = env
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err := cmd.Run()
		if !want {
			if err == nil || out.Len() != 0 || bytes.Contains(stderr.Bytes(), []byte("panic:")) {
				t.Fatal("invalid graph preparation accepted", stderr.String())
			}
			return nil
		}
		if err != nil {
			t.Fatal("prepare-graph executable", err, stderr.String())
		}
		var result struct {
			Status, Publication, Snapshot string
			Jobs                          []string
		}
		if err = json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != "scheduled" || result.Publication != publication || result.Snapshot != target || len(result.Jobs) != 1 {
			t.Fatal("preparation response", err, out.String())
		}
		return result.Jobs
	}
	assertAbsent := func() {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM snapshots WHERE publication_id=$1`, publication).Scan(&n); err != nil || n != 0 {
			t.Fatal("preflight failure reserved target", n, err)
		}
	}
	wrong := append([]string(nil), args...)
	wrong[10] = "scope:wrong"
	invoke(wrong, false)
	assertAbsent()
	wrong = append([]string(nil), args...)
	wrong[8] = "snapshot:wrong"
	invoke(wrong, false)
	assertAbsent()
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, source.SourceJobID); err != nil {
		t.Fatal(err)
	}
	invoke(args, false)
	assertAbsent()
	if _, err = db.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, source.SourceJobID); err != nil {
		t.Fatal(err)
	}
	first := invoke(args, true)
	second := invoke(args, true)
	if first[0] != second[0] {
		t.Fatal("preparation replay changed child identity")
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM snapshot_read_leases WHERE corpus_id=$1 AND owner_id LIKE 'graph-prepare:%'`, corpus).Scan(&count); err != nil || count != 0 {
		t.Fatal("preparation leaked pins", count, err)
	}
	in, err := repo.LoadGraphJobInventory(ctx, corpus, publication)
	if err != nil || len(in.Assignments) != 1 || in.Assignments[0].JobID != first[0] {
		t.Fatal("durable preparation inventory", err)
	}
	binding, err := repo.LoadGraphSourceBinding(ctx, corpus, publication, source.SourceJobID)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := workflows.ReadGraphInventoryInputs(ctx, repo, files, in)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := repo.PinActiveSnapshot(ctx, corpus, "pin:prepare-test", "owner:prepare-test", 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), pin.LeaseID, pin.OwnerID)
	selected, err := repo.LoadPublishedGraphSources(ctx, pin, source.AuthScope)
	if err != nil || len(selected) != 1 || !proto.Equal(selected[0].Original, source.Original) {
		t.Fatal("source selection", err)
	}
	badPin := pin
	badPin.OwnerID = "owner:wrong"
	if _, err = repo.LoadPublishedGraphSources(ctx, badPin, source.AuthScope); err == nil {
		t.Fatal("wrong lease owner admitted")
	}
	a := in.Assignments[0]
	prepared := &workflows.PreparedGraphAssembly{Plan: a.Plan, Reference: a.Reference}
	input := inputs[source.SourceJobID]
	t.Log("actual prepare-graph: fresh reservation, complete source binding, atomic child scheduling, exact replay, wrong scope/base/cancellation rejected")
	checkNativeGraphExecution(t, ctx, repo, db, files, artifacts, pin, prepared, binding, input.Source, input.RegistryView, ontology)
}
