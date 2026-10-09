// Opt-in PostgreSQL replay test uses a unique disposable schema, real migrations,
// closed/reopened pools and recreated gateways. Tests immutable first-writer wins,
// corrupted usage rejection and continued semantic validation, not model quality.
package inference

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func TestExtractReplayPostgresRestart(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	schema := fmt.Sprintf("semantic_replay_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_, e := admin.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE")
		if e != nil {
			t.Error(e)
		}
	}()
	endpoint, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Scheme != "postgres" && endpoint.Scheme != "postgresql" {
		t.Fatal("integration fixture requires a PostgreSQL URL")
	}
	query := endpoint.Query()
	query.Set("search_path", schema)
	endpoint.RawQuery = query.Encode()
	open := func() *postgres.Repository {
		r, e := postgres.Open(ctx, postgres.Config{DSN: endpoint.String(), MaxConnections: 4, HealthTimeout: 3 * time.Second})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	repo := open()
	migrations, _ := filepath.Abs("../../../../../migrations")
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		repo.Close()
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		repo.Close()
		t.Fatal(err)
	}
	p := &providerDouble{raw: validRawProposal()}
	base, request := semanticFixture(p)
	s := newReplayService(t, p, base.config, repo)
	first, err := s.ExtractBatch(ctx, request)
	if err != nil || first.Results[0].GetProposal() == nil {
		repo.Close()
		t.Fatalf("initial completion failed: %v %+v", err, first)
	}
	repo.Close()
	repo = open()
	defer repo.Close()
	p2 := &providerDouble{err: errors.New("provider unavailable after restart")}
	s = newReplayService(t, p2, base.config, repo)
	replayed, err := s.ExtractBatch(ctx, request)
	if err != nil || !proto.Equal(first.Results[0], replayed.Results[0]) || p2.callCount() != 0 {
		t.Fatalf("durable restart failed: %v", err)
	}
	key := semanticHash("9")
	var wg sync.WaitGroup
	results := make(chan domain.ModelCompletion, 2)
	failures := make(chan error, 2)
	for _, raw := range []string{`{"sample":1}`, `{"sample":2}`} {
		wg.Add(1)
		go func(raw string) {
			defer wg.Done()
			v, e := repo.SaveModelCompletion(ctx, key, domain.ModelCompletion{JSON: []byte(raw), InputTokens: 5, OutputTokens: 2})
			results <- v
			failures <- e
		}(raw)
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	var winner string
	for v := range results {
		if winner != "" && winner != string(v.JSON) {
			t.Fatal("concurrent completion writers returned different winners")
		}
		winner = string(v.JSON)
	}
	if _, err = admin.Exec(ctx, "UPDATE "+quoted+".semantic_completions SET input_tokens=99 WHERE replay_key=$1", key.Sha256); err == nil {
		t.Fatal("checkpoint mutation allowed")
	}
	if _, err = admin.Exec(ctx, "ALTER TABLE "+quoted+".semantic_completions DISABLE TRIGGER semantic_completions_immutable"); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "UPDATE "+quoted+".semantic_completions SET input_tokens=input_tokens+1 WHERE replay_key=$1", key.Sha256); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.LoadModelCompletion(ctx, key); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatalf("usage corruption escaped hash check: %v", err)
	}
	t.Log("migration replay, closed/reopened pool + gateway recovery, concurrent first-writer replay, immutable rows and usage corruption checks passed")
}
