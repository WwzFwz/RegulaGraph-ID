// Isolates indexing integration fixtures from PostgreSQL adapter tests that
// truncate their own default schema. Each run owns a schema and drops only that
// explicitly allocated namespace; shared databases are never reset here.
package indexing

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/url"
	"testing"
	"time"
)

func isolatedIndexTestDSN(t *testing.T, ctx context.Context, dsn string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("integration test requires PostgreSQL URI DSN")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := pgx.Identifier{fmt.Sprintf("index_fixture_%d", time.Now().UnixNano())}
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+name.Sanitize()); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, "DROP SCHEMA "+name.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		conn.Close(cleanup)
	})
	query := parsed.Query()
	query.Set("search_path", name[0])
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
