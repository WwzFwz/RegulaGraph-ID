// Invokes the actual operator CLI against a freshly published native graph.
// The fixture persists registered in-memory artifacts to its dedicated FileStore;
// the executable must perform its own pin, catalog routing, alias lookup, traversal
// and hydration. No native model endpoint is supplied for graph-only evidence.
// This proves executable wiring on synthetic data, not model/latency acceptance.
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
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

func checkPublishedGraphCLI(t *testing.T, ctx context.Context, repo *postgres.Repository, backend *neo4j.Store, index *domain.PinnedIndex, scope, question string, artifacts indexMemoryArtifacts) {
	t.Helper()
	binary := os.Getenv("REGULAGRAPH_TEST_QUERY_CLI")
	if binary == "" {
		t.Log("actual graph CLI executable check not configured")
		return
	}
	root := os.Getenv("REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT")
	files, err := storage.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	for id, raw := range artifacts {
		ref, e := repo.LoadArtifact(ctx, index.Pin.CorpusID, id)
		if errors.Is(e, postgres.ErrNotFound) {
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		if _, e = files.Put(ctx, ref, bytes.NewReader(raw)); e != nil {
			t.Fatal("persist CLI fixture artifact", id, e)
		}
	}
	configuration := map[string]any{"schema_version": 1, "corpus": index.Pin.CorpusID, "endpoint": backend.Endpoint(), "database": backend.Database(), "linking": map[string]any{"namespaces": []map[string]string{{"entity_type": "organization", "scope": "ID:national"}}, "maximum_query_bytes": 4096, "maximum_phrase_tokens": 4, "maximum_phrases": 128, "maximum_lookups": 128, "maximum_aliases_per_lookup": 32, "maximum_seeds": 64}, "traversal": map[string]any{"maximum_hops": 3, "maximum_paths": 100, "assertions": 128, "supports": 256, "bytes": 1 << 20}}
	raw, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "graph.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	queryDSN := os.Getenv("REGULAGRAPH_TEST_QUERY_DSN")
	if queryDSN == "" {
		t.Fatal("isolated CLI fixture DSN required")
	}
	env := append(os.Environ(), "REGULAGRAPH_QUERY_CORPUS_ID="+index.Pin.CorpusID, "REGULAGRAPH_BUILD_ID=fixture:cli-native", "REGULAGRAPH_POSTGRES_DSN="+queryDSN, "REGULAGRAPH_ARTIFACTS_DIR="+root, "REGULAGRAPH_QUERY_NATIVE_ENDPOINT=", "REGULAGRAPH_QDRANT_URL="+index.Binding.Endpoint, "REGULAGRAPH_QDRANT_API_KEY=", "REGULAGRAPH_QUERY_GRAPH_CONFIG="+path, "REGULAGRAPH_QUERY_GRAPH_CONFIG_SHA256="+fmt.Sprintf("%x", sha256.Sum256(raw)), "REGULAGRAPH_NEO4J_USERNAME=neo4j", "REGULAGRAPH_NEO4J_PASSWORD="+os.Getenv("REGULAGRAPH_TEST_NEO4J_PASSWORD"), "REGULAGRAPH_QUERY_AUTH_SCOPE="+scope, "REGULAGRAPH_QUERY_RERANK_MANIFEST=", "REGULAGRAPH_QUERY_RERANK_MANIFEST_SHA256=")
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(call, binary, "query-evidence", "-question", question, "-as-of", "2026-01-01", "-profile", "graph", "-limit", "8", "-timeout", "10s")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		t.Fatal("actual graph CLI failed", err, stderr.String())
	}
	var envelope struct {
		Mode     string          `json:"mode"`
		Evidence json.RawMessage `json:"evidence"`
	}
	if err = json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope.Mode != "evidence" {
		t.Fatal("CLI envelope", err)
	}
	bundle := new(pb.EvidenceBundle)
	if err = protojson.Unmarshal(envelope.Evidence, bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Snapshot.GetSnapshotId() != index.Snapshot.SnapshotId || len(bundle.Items) != 1 || len(bundle.Items[0].GraphPaths) == 0 || bundle.Completeness != pb.Completeness_COMPLETENESS_PARTIAL {
		t.Fatal("CLI graph lost snapshot/source proof")
	}
	t.Log("actual CLI graph evidence PASS: independent pin + exact alias + Neo4j/Qdrant source hydration, no native endpoint")
}
