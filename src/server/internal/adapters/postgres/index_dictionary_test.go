// Verifies lexical allocator input canonicalization without a database, plus the
// append-only/replay behavior against a disposable PostgreSQL when an explicit
// test DSN is supplied. These checks do not measure BM25 relevance or latency.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestValidateLexicalTermsCanonicalDigest(t *testing.T) {
	a, hashA, err := validateLexicalTerms("corpus:one", "analyzer:v1", "operation:one", 1,
		[]string{"pasal", "12/2020", "tidak"})
	if err != nil {
		t.Fatal(err)
	}
	b, hashB, err := validateLexicalTerms("corpus:one", "analyzer:v1", "operation:one", 1,
		[]string{"tidak", "pasal", "12/2020"})
	if err != nil || hashA != hashB || !reflect.DeepEqual(a, b) {
		t.Fatalf("order-independent digest: %v %v %q %q %v", a, b, hashA, hashB, err)
	}
	for _, terms := range [][]string{{}, {"pasal", "pasal"}, {""}, {"a\x00b"}, {"\xff"}} {
		if _, _, err := validateLexicalTerms("corpus:one", "analyzer:v1", "operation:one", 1, terms); err == nil {
			t.Fatalf("accepted invalid terms %q", terms)
		}
	}
	if _, _, err := validateLexicalTerms("corpus:one", "analyzer:v1", "operation:one", 1,
		[]string{"x", "xy"}); err != nil {
		t.Fatalf("length-prefix boundary rejected: %v", err)
	}
}

func TestLexicalDictionaryAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("REGULAGRAPH_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	migrationPath, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrationPath)); err != nil {
		t.Fatal(err)
	}
	corpus := fmt.Sprintf("corpus:lexical-test:%d", time.Now().UnixNano())
	first, revision, err := repo.AllocateLexicalTerms(ctx, corpus, "analyzer:v1", "operation:first", 1,
		[]string{"pasal", "izin"})
	if err != nil || revision != 2 || !reflect.DeepEqual(first, []LexicalTerm{{"pasal", 2}, {"izin", 1}}) {
		t.Fatalf("initial allocation: %v revision=%d err=%v", first, revision, err)
	}
	second, revision, err := repo.AllocateLexicalTerms(ctx, corpus, "analyzer:v1", "operation:second", 2,
		[]string{"izin", "tidak"})
	if err != nil || revision != 3 || !reflect.DeepEqual(second, []LexicalTerm{{"izin", 1}, {"tidak", 3}}) {
		t.Fatalf("append allocation: %v revision=%d err=%v", second, revision, err)
	}
	replayed, oldRevision, err := repo.AllocateLexicalTerms(ctx, corpus, "analyzer:v1", "operation:first", 1,
		[]string{"izin", "pasal"})
	if err != nil || oldRevision != 2 || !reflect.DeepEqual(replayed, []LexicalTerm{{"izin", 1}, {"pasal", 2}}) {
		t.Fatalf("historical replay: %v revision=%d err=%v", replayed, oldRevision, err)
	}
	if _, _, err = repo.AllocateLexicalTerms(ctx, corpus, "analyzer:v1", "operation:first", 1,
		[]string{"izin", "other"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed operation payload: %v", err)
	}
	if _, _, err = repo.AllocateLexicalTerms(ctx, corpus, "analyzer:v1", "operation:stale", 2,
		[]string{"new"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	noChange, unchangedRevision, err := repo.AllocateLexicalTerms(ctx, corpus, "analyzer:v1", "operation:existing", 3,
		[]string{"pasal", "izin"})
	if err != nil || unchangedRevision != 3 || !reflect.DeepEqual(noChange, first) {
		t.Fatalf("existing terms advanced revision: %v revision=%d err=%v", noChange, unchangedRevision, err)
	}
	old, err := repo.LoadLexicalDictionary(ctx, corpus, "analyzer:v1", 2, 3)
	if err != nil || !reflect.DeepEqual(old, []LexicalTerm{{"izin", 1}, {"pasal", 2}}) {
		t.Fatalf("pinned old dictionary: %v err=%v", old, err)
	}
	if _, err = repo.LoadLexicalDictionary(ctx, corpus, "analyzer:v1", 3, 2); !errors.Is(err, ErrResultLimit) {
		t.Fatalf("unbounded dictionary read: %v", err)
	}
	if _, err = repo.LoadLexicalDictionary(ctx, corpus, "analyzer:v1", 4, 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("future dictionary revision: %v", err)
	}
	other, otherRevision, err := repo.AllocateLexicalTerms(ctx, corpus, "analyzer:v2", "operation:first", 1,
		[]string{"pasal"})
	if err != nil || otherRevision != 2 || !reflect.DeepEqual(other, []LexicalTerm{{"pasal", 1}}) {
		t.Fatalf("analyzer isolation: %v revision=%d err=%v", other, otherRevision, err)
	}
	if _, err = repo.pool.Exec(ctx, `DELETE FROM lexical_dictionary_terms WHERE corpus_id=$1`, corpus); err == nil {
		t.Fatal("append-only dictionary accepted DELETE")
	}
	_, revision, err = repo.AllocateLexicalTerms(ctx, corpus, "analyzer:concurrent", "operation:seed", 1,
		[]string{"seed"})
	if err != nil || revision != 2 {
		t.Fatalf("concurrent dictionary seed: revision=%d err=%v", revision, err)
	}
	type concurrentResult struct {
		operation string
		err       error
	}
	start := make(chan struct{})
	results := make(chan concurrentResult, 2)
	for _, operation := range []string{"operation:race-a", "operation:race-b"} {
		go func(operation string) {
			<-start
			_, _, callErr := repo.AllocateLexicalTerms(ctx, corpus, "analyzer:concurrent", operation, 2,
				[]string{operation})
			results <- concurrentResult{operation, callErr}
		}(operation)
	}
	close(start)
	won := 0
	loser := ""
	for range 2 {
		result := <-results
		if result.err == nil {
			won++
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.Is(result.err, ErrConflict) && !(errors.As(result.err, &pgErr) && pgErr.Code == "40001") {
			t.Fatalf("unexpected concurrent allocation failure: %v", result.err)
		}
		loser = result.operation
	}
	if won != 1 || loser == "" {
		t.Fatalf("stale concurrent revision admitted: winners=%d loser=%q", won, loser)
	}
	_, revision, err = repo.AllocateLexicalTerms(ctx, corpus, "analyzer:concurrent", loser, 0,
		[]string{loser})
	if err != nil || revision != 4 {
		t.Fatalf("bounded caller retry after concurrent conflict: revision=%d err=%v", revision, err)
	}
	all, err := repo.LoadLexicalDictionary(ctx, corpus, "analyzer:concurrent", 4, 3)
	if err != nil || len(all) != 3 || all[0].ID != 1 || all[1].ID != 2 || all[2].ID != 3 {
		t.Fatalf("concurrent IDs not contiguous: %v err=%v", all, err)
	}
}
