// Exercises actual initial snapshot preparation against isolated PostgreSQL and
// FileStore: selected CHUNK sources bind deterministically, facts are observed
// rather than handwritten, replay is exact and source/config drift cannot reuse
// the reservation. Synthetic legal records are not a quality evaluation corpus.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
)

func TestInitialSourceSnapshotAgainstPostgres(t *testing.T) {
	runInitialIndexPublication(t, false, true, "snapshot-preparation")
}

func checkInitialSnapshotPreparation(t *testing.T, ctx context.Context, repo *postgres.Repository, artifacts indexMemoryArtifacts, corpus, publication, generation, scope, job string, source *pb.ArtifactRef) {
	t.Helper()
	files, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	cfg := InitialSourceSnapshotConfig{CorpusID: corpus, PublicationID: publication, GenerationID: generation, AuthScope: scope}
	inputs := []InitialIndexSource{{SourceJobID: job, DocumentBatch: source}}
	result, err := PrepareInitialSourceSnapshot(ctx, repo, artifacts, files, cfg, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.ManifestHash.Sha256 != fmt.Sprintf("%x", sha256.Sum256(result.ManifestBytes)) || len(result.Sources) != 1 || proto.Equal(result.Sources[0].DocumentBatch, source) {
		t.Fatal("snapshot/provenance preparation mismatch")
	}
	var facts map[string]any
	if err = json.Unmarshal(result.ManifestBytes, &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 8 || facts["documents"] != float64(1) || facts["chunks"] != float64(2) || facts["canonical_entities"] != float64(0) || facts["graph_edges"] != float64(0) {
		t.Fatal("unexpected corpus facts", facts)
	}
	if facts["snapshot_id"] != result.Snapshot.SnapshotId || facts["representation_generation"] != generation {
		t.Fatal("facts not pinned")
	}
	if _, err = files.ReadVerified(ctx, result.Manifest, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err = repo.VerifyIndexSourceCheckpoint(ctx, corpus, job, result.Sources[0].DocumentBatch); err != nil {
		t.Fatal(err)
	}
	replay, err := PrepareInitialSourceSnapshot(ctx, repo, artifacts, files, cfg, inputs)
	if err != nil || !proto.Equal(result.Snapshot, replay.Snapshot) || !proto.Equal(result.Sources[0].DocumentBatch, replay.Sources[0].DocumentBatch) {
		t.Fatal("preparation replay drift", err)
	}
	changed := cfg
	changed.GenerationID += "-changed"
	if _, err = PrepareInitialSourceSnapshot(ctx, repo, artifacts, files, changed, inputs); err == nil {
		t.Fatal("changed generation reused reservation")
	}
	if _, err = PrepareInitialSourceSnapshot(ctx, repo, artifacts, files, cfg, append(inputs, inputs[0])); err == nil {
		t.Fatal("duplicate source selection accepted")
	}
	changed = cfg
	changed.AuthScope += "-changed"
	if _, err = PrepareInitialSourceSnapshot(ctx, repo, artifacts, files, changed, inputs); err == nil {
		t.Fatal("scope mismatch accepted")
	}
	saved := artifacts[source.ArtifactId]
	artifacts[source.ArtifactId] = []byte("corrupt")
	if _, err = PrepareInitialSourceSnapshot(ctx, repo, artifacts, files, cfg, inputs); err == nil {
		t.Fatal("corrupt selected source accepted")
	}
	artifacts[source.ArtifactId] = saved
}
