// Runs the actual publish-graph executable on Rust-produced graph artifacts and
// isolated PG/Neo4j/Qdrant. Fresh publication must activate both backends; exact
// replay does not create a new generation, and configuration drift is rejected.
// Source/semantic labels remain fixtures, not corpus quality acceptance.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

func checkPublishGraphCLI(t *testing.T, ctx context.Context, repo *postgres.Repository, db *pgx.Conn, authority *postgres.GraphJobAdmission, pin domain.SnapshotPin, binary string, graph *neo4j.Store, prepared *workflows.PreparedGraphOutputs, scope string) {
	t.Helper()
	b := graph.Binding()
	var target string
	if err := db.QueryRow(ctx, `SELECT snapshot_id FROM snapshots WHERE publication_id=$1`, b.PublicationID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("REGULAGRAPH_TEST_GRAPH_PUBLISH_FAILURES") == "1" {
		backend, _ := reuseBackend(t, ctx, repo, pin)
		failed := &failedIndexReuseReadback{Store: backend}
		if result, err := PublishPreparedGraph(ctx, repo, authority, graph, failed, prepared, pin, target); !errors.Is(err, errIndexReuseReadback) || result != nil {
			t.Fatal("index failure exposed success", err)
		}
		active, _, err := repo.ActiveSnapshot(ctx, b.CorpusID)
		if err != nil || active != pin.SnapshotID {
			t.Fatal("partial publication changed active snapshot", err)
		}
		var count int
		if err = db.QueryRow(ctx, `SELECT count(*) FROM backend_receipts WHERE publication_id=$1 AND backend=$2`, b.PublicationID, int16(pb.BackendKind_BACKEND_KIND_QDRANT)).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed index generated receipt", err)
		}
		t.Log("production graph publisher: index readback failure preserves parent pointer and leaves graph intent recoverable")
	}
	path, err := filepath.Abs("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT")
	env := append(os.Environ(), "REGULAGRAPH_POSTGRES_DSN="+os.Getenv("REGULAGRAPH_TEST_QUERY_DSN"), "REGULAGRAPH_ARTIFACTS_DIR="+root, "REGULAGRAPH_NEO4J_URI="+graph.Endpoint(), "REGULAGRAPH_NEO4J_DATABASE="+graph.Database(), "REGULAGRAPH_NEO4J_USERNAME=neo4j", "REGULAGRAPH_NEO4J_PASSWORD="+os.Getenv("REGULAGRAPH_TEST_NEO4J_PASSWORD"), "REGULAGRAPH_QDRANT_URL="+os.Getenv("REGULAGRAPH_TEST_QDRANT_ENDPOINT"), "REGULAGRAPH_QDRANT_API_KEY=", "REGULAGRAPH_ONTOLOGY_PATH="+path, "REGULAGRAPH_ONTOLOGY_SHA256="+fmt.Sprintf("%x", sha256.Sum256(raw)))
	args := []string{"publish-graph", "-corpus", b.CorpusID, "-publication", b.PublicationID, "-snapshot", target, "-generation", b.Generation, "-auth-scope", scope, "-timeout", "30s"}
	invoke := func(a []string, want bool) *pb.SnapshotRef {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, a...)
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if !want {
			if err == nil || stdout.Len() != 0 || bytes.Contains(stderr.Bytes(), []byte("panic:")) {
				t.Fatal("invalid operator graph request succeeded")
			}
			return nil
		}
		if err != nil {
			t.Fatal("actual publish-graph", err, stderr.String())
		}
		var result struct {
			Status   string
			Snapshot json.RawMessage
		}
		if err = json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Status != "published" {
			t.Fatal("publication envelope", err)
		}
		s := new(pb.SnapshotRef)
		if err = protojson.Unmarshal(result.Snapshot, s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	wrong := append([]string(nil), args...)
	wrong[10] = "scope:wrong"
	invoke(wrong, false)
	first := invoke(args, true)
	second := invoke(args, true)
	if !proto.Equal(first, second) || first.SnapshotId != target || first.Sequence != b.Sequence {
		t.Fatal("publication replay changed identity")
	}
	wrong = append([]string(nil), args...)
	wrong[8] = "generation:wrong"
	invoke(wrong, false)
	// Fixture corruption must be reported as an integrity failure, not a panic
	// while accessing absent nested manifest fields on published replay.
	var manifestBytes []byte
	if err = db.QueryRow(ctx, `SELECT manifest_payload FROM snapshots WHERE publication_id=$1`, b.PublicationID).Scan(&manifestBytes); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if _, e := db.Exec(ctx, `UPDATE snapshots SET manifest_payload=$2 WHERE publication_id=$1`, b.PublicationID, manifestBytes); e != nil {
				t.Error(e)
			}
		}()
		if _, e := db.Exec(ctx, `UPDATE snapshots SET manifest_payload=$2 WHERE publication_id=$1`, b.PublicationID, []byte{0x0a, 0x00}); e != nil {
			t.Fatal(e)
		}
		invoke(args, false)
	}()
	stored, err := repo.LoadGraphGeneration(ctx, b.CorpusID, b.PublicationID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := DescribePreparedGraph(prepared, graph)
	if err != nil || stored.BindingHash != want.BindingHash || stored.InventoryHash != want.InventoryHash {
		t.Fatal("operator changed admitted graph", err)
	}
	var leases int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM snapshot_read_leases WHERE owner_id LIKE 'graph-publish:%'`).Scan(&leases); err != nil || leases != 0 {
		t.Fatal("publisher leaked lease", err)
	}
	t.Log("actual publish-graph CLI: fresh graph/index activation, identical replay, scope/generation rejection and lease cleanup PASS")
}

func TestNativeGraphPublicationFailureRecovery(t *testing.T) {
	if os.Getenv("REGULAGRAPH_TEST_NATIVE_GRAPH") != "1" || os.Getenv("REGULAGRAPH_TEST_PUBLISH_GRAPH_CLI") == "" {
		t.Skip("native graph publisher opt-in required")
	}
	t.Setenv("REGULAGRAPH_TEST_GRAPH_PUBLISH_FAILURES", "1")
	TestNativeGraphAssemblyPipeline(t)
}
