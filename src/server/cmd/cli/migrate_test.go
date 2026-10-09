// Tests explicit migration admission, safe errors and actual transactional replay.
// Database coverage uses an isolated disposable schema; no production data or down
// migration is touched. These checks establish bootstrap behavior, not load targets.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMigrateAdmissionAndSafeFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0001_test.up.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REGULAGRAPH_POSTGRES_DSN", "secret-dsn")
	calls := 0
	apply := func(ctx context.Context, dsn string, files fs.FS) error {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		return errors.New("secret-dsn password")
	}
	for _, args := range [][]string{{"-timeout", "0s"}, {"extra"}, {"-dir", t.TempDir()}, {"-unknown"}} {
		var out, errOut bytes.Buffer
		if code := runMigrateWith(context.Background(), args, &out, &errOut, apply); code != 2 {
			t.Fatalf("invalid invocation code=%d", code)
		}
	}
	if calls != 0 {
		t.Fatal("invalid invocation opened database")
	}
	var out, errOut bytes.Buffer
	if code := runMigrateWith(context.Background(), []string{"-dir", dir}, &out, &errOut, apply); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if strings.Contains(errOut.String(), "secret-dsn") || strings.Contains(errOut.String(), "password") || out.Len() != 0 {
		t.Fatal("credential leak or false success")
	}
}

func TestMigrateActualPostgresReplayAndRollback(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := pgx.Identifier{fmt.Sprintf("cli_migrate_%d", time.Now().UnixNano())}
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), "DROP SCHEMA "+schema.Sanitize()+" CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		t.Fatal("integration test requires a PostgreSQL URI")
	}
	query := u.Query()
	query.Set("search_path", schema[0])
	u.RawQuery = query.Encode()
	t.Setenv("REGULAGRAPH_POSTGRES_DSN", u.String())
	dir := t.TempDir()
	path := filepath.Join(dir, "0001_first.up.sql")
	if err = os.WriteFile(path, []byte("CREATE TABLE sample(id integer PRIMARY KEY); INSERT INTO sample VALUES(1);"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(want int) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := run(ctx, []string{"migrate", "-dir", dir}, &out, &errOut)
		if code != want {
			t.Fatalf("code=%d want=%d error=%s", code, want, errOut.String())
		}
		if want == 0 && !strings.Contains(out.String(), `"status":"applied"`) {
			t.Fatal("missing success")
		}
	}
	invoke(0)
	invoke(0)
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM "+schema.Sanitize()+".sample").Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay changed data: %d %v", count, err)
	}
	if err = os.WriteFile(path, []byte("SELECT 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(1)
	if err = os.WriteFile(path, []byte("CREATE TABLE sample(id integer PRIMARY KEY); INSERT INTO sample VALUES(1);"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "0002_bad.up.sql"), []byte("INSERT INTO sample VALUES(2); SELECT missing_column FROM sample;"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(1)
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM "+schema.Sanitize()+".sample").Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed migration was not rolled back: %d %v", count, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		var out, errOut bytes.Buffer
		if code := run(ctx, []string{"migrate", "-dir", "../../../../migrations"}, &out, &errOut); code != 0 {
			t.Fatalf("repository migrations: %s", errOut.String())
		}
	}
}
