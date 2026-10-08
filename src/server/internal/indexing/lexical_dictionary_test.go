// Verifies dictionary preparation against real PostgreSQL and FileStore, including
// interrupted registration and historical replay after another allocation. This
// is storage/identity evidence, not corpus relevance or benchmark acceptance.
package indexing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

type interruptedDictionaryRegistry struct {
	*postgres.Repository
	fail bool
}

func (r *interruptedDictionaryRegistry) RegisterArtifact(ctx context.Context, corpus string, ref *pb.ArtifactRef) error {
	if r.fail {
		r.fail = false
		return errors.New("injected registration interruption")
	}
	return r.Repository.RegisterArtifact(ctx, corpus, ref)
}

func TestPrepareLexicalDictionaryAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL test DSN required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	repo, err := postgres.Open(ctx, postgres.Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	migrationPath, err := filepath.Abs("../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrationPath)); err != nil {
		t.Fatal(err)
	}
	files, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	corpus := fmt.Sprintf("corpus:dictionary-prepare:%d", time.Now().UnixNano())
	terms := make([]string, 5001)
	for i := range terms {
		terms[i] = fmt.Sprintf("term%05d", i)
	}
	registry := &interruptedDictionaryRegistry{Repository: repo, fail: true}
	if ref, err := PrepareLexicalDictionary(ctx, registry, files, corpus, terms); err == nil || ref != nil {
		t.Fatal("interruption became success")
	}
	first, err := PrepareLexicalDictionary(ctx, registry, files, corpus, terms)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.AllocateLexicalTerms(ctx, corpus, domain.LexicalAnalyzerV1, "op:intervening", 0, []string{"zzz"}); err != nil {
		t.Fatal(err)
	}
	replay, err := PrepareLexicalDictionary(ctx, registry, files, corpus, terms)
	if err != nil || !proto.Equal(first, replay) {
		t.Fatal("replay changed historical artifact", err)
	}
	raw, err := files.ReadVerified(ctx, replay, replay.ByteSize)
	if err != nil {
		t.Fatal(err)
	}
	var artifact pb.LexicalDictionaryArtifact
	if err = domain.DecodeWire(raw, &artifact, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	if len(artifact.Entries) != 5001 || artifact.RegistryRevision != 3 {
		t.Fatal("partial population or future revision exported")
	}
	registered, err := repo.LoadArtifact(ctx, corpus, replay.ArtifactId)
	if err != nil || !proto.Equal(registered, replay) {
		t.Fatal("missing registered bytes", err)
	}
	for _, bad := range [][]string{{"b", "a"}, {"a", "a"}, {"two words"}, {""}, {string([]byte{255})}} {
		if _, err = PrepareLexicalDictionary(ctx, registry, files, corpus, bad); err == nil {
			t.Fatal("bad vocabulary accepted")
		}
	}
}

func TestRustPopulationStatisticsHandoff(t *testing.T) {
	directory := os.Getenv("REGULAGRAPH_LEXICAL_FIXTURE_DIR")
	if directory == "" {
		t.Skip("Rust-Go-Rust CLI fixture required")
	}
	files, err := storage.NewFileStore(filepath.Join(directory, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	read := func(name string, message proto.Message) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = domain.DecodeWire(raw, message, domain.DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
	}
	load := func(name string, message proto.Message) {
		t.Helper()
		ref := new(pb.ArtifactRef)
		read(name, ref)
		raw, err := files.ReadVerified(context.Background(), ref, 16<<20)
		if err != nil {
			t.Fatal(err)
		}
		if err = domain.DecodeWire(raw, message, domain.DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
	}
	dictionary, stats, source, snapshot := new(pb.LexicalDictionaryArtifact), new(pb.LexicalStatisticsArtifact), new(pb.DocumentBatch), new(pb.SnapshotRef)
	load("dictionary-ref.pb", dictionary)
	load("statistics-ref.pb", stats)
	read("source.pb", source)
	read("snapshot.pb", snapshot)
	checked, err := domain.CheckLexicalDictionaryArtifact(dictionary, source.Meta.CorpusId, nil, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err = domain.CheckLexicalStatisticsArtifact(stats, checked, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	if stats.DocumentCount != uint64(len(source.Chunks)) || !proto.Equal(stats.PopulationSnapshot, snapshot) || stats.InputPolicy != "structure-labels-v1" || stats.TotalTokens == 0 {
		t.Fatal("population or rendering identity lost across runtimes")
	}
}
