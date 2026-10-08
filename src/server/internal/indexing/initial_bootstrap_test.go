// Verifies frozen-statistics import, generation construction and atomic child
// scheduling against real PostgreSQL/FileStore. Replays preserve IDs and frozen
// dependencies; changed model/generation cannot rewrite an existing inventory.
// Fixture vectors/statistics are synthetic, so this is integration evidence only.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

func TestInitialIndexBootstrapAgainstPostgres(t *testing.T) {
	runInitialIndexPublication(t, false, true, "bootstrap")
}

func checkInitialIndexBootstrap(t *testing.T, ctx context.Context, repo *postgres.Repository, files *storage.FileStore, artifacts indexMemoryArtifacts, binding domain.IndexCatalogBinding, snapshot *pb.SnapshotRef, scope, job string, source, dictionary, stats *pb.ArtifactRef) {
	t.Helper()
	for id, raw := range artifacts {
		ref, err := repo.LoadArtifact(ctx, snapshot.CorpusId, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = files.Put(ctx, ref, bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate the real Rust freeze handoff: bytes exist but are not registered.
	unregistered := new(pb.LexicalStatisticsArtifact)
	if err := proto.Unmarshal(artifacts[stats.ArtifactId], unregistered); err != nil {
		t.Fatal(err)
	}
	unregistered.Meta.RecordId += "-import"
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(unregistered)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	ref := &pb.ArtifactRef{ArtifactId: unregistered.Meta.RecordId, SchemaVersion: 1, MediaType: stats.MediaType, ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: hash}, StorageKey: "sha256/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".bin"}
	if _, err = files.Put(ctx, ref, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.LoadArtifact(ctx, snapshot.CorpusId, ref.ArtifactId); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("statistics already registered", err)
	}
	cfg := InitialIndexBootstrapConfig{PublicationID: binding.PublicationID, Endpoint: binding.Endpoint, Collection: binding.Collection, AuthScope: scope, OntologyVersion: binding.Generation.OntologyVersion, Snapshot: snapshot, Model: binding.Generation.DenseManifest, Dictionary: dictionary, Statistics: ref, ChunksPerBatch: 1}
	inputs := []InitialIndexSource{{SourceJobID: job, DocumentBatch: source}}
	inventory, err := BootstrapInitialIndex(ctx, repo, files, files, cfg, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Assignments) != 2 {
		t.Fatal("incomplete scheduled chunk set")
	}
	replay, err := BootstrapInitialIndex(ctx, repo, files, files, cfg, inputs)
	if err != nil || replay.Assignments[0].JobID != inventory.Assignments[0].JobID {
		t.Fatal("bootstrap replay", err)
	}
	if _, err = RestoreInitialIndexPlans(ctx, repo, files, replay); err != nil {
		t.Fatal("scheduled inventory cannot restore", err)
	}
	changed := cfg
	changed.Model = proto.Clone(cfg.Model).(*pb.ModelManifest)
	changed.Model.ModelId += "-changed"
	if _, err = BootstrapInitialIndex(ctx, repo, files, files, changed, inputs); err == nil {
		t.Fatal("model drift replaced inventory")
	}
	stored, err := repo.LoadIndexJobInventory(ctx, binding.PublicationID)
	if err != nil || !proto.Equal(stored.Binding.Generation, inventory.Binding.Generation) {
		t.Fatal("failed bootstrap changed inventory", err)
	}
	bad := &pb.DependencyManifest{ArtifactId: ref.ArtifactId, ProducerManifest: inventory.Assignments[0].Plan.Producer, Dependencies: []*pb.Dependency{{DependencyId: "artifact:foreign", Fingerprint: ref.ContentHash}}}
	if err = repo.EnsureArtifactDependencyManifest(ctx, snapshot.CorpusId, ref.ArtifactId, bad); !errors.Is(err, postgres.ErrConflict) {
		t.Fatal("statistics dependencies overwritten", err)
	}
	// The conflict must preserve exact replay of the original source dependency set.
	if _, err = BootstrapInitialIndex(ctx, repo, files, files, cfg, inputs); err != nil {
		t.Fatal("dependency conflict damaged original preparation", err)
	}
}
