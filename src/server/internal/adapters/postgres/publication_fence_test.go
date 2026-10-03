// Checks database exclusion of simultaneous open publications, then injects a
// changed publisher epoch to test defensive activation fencing. PostgreSQL must
// reject activation despite valid backend acknowledgements;
// an already published historical retry stays idempotent. This is a consistency
// test, not a production latency or recovery benchmark.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublicationRejectsSupersededPublisherAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dir, _ := filepath.Abs("../../../../../migrations")
	if err = r.ApplyMigrations(ctx, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	corpus := fmt.Sprintf("corpus:fence:%d", time.Now().UnixNano())
	stage := func(id string) string {
		t.Helper()
		pub := "publication:" + id + corpus
		snap := "snapshot:" + id + corpus
		reservation, e := r.ReservePublication(ctx, pub, "", corpus, snap, "")
		if e != nil {
			t.Fatal(e)
		}
		if e = r.StagePublication(ctx, publicationFixture(corpus, pub, snap, reservation.Sequence, reservation.Fence, nil)); e != nil {
			t.Fatal(e)
		}
		if e = r.RecordBackendReceipt(ctx, receiptFixture(pub, reservation.Fence)); e != nil {
			t.Fatal(e)
		}
		return pub
	}
	old := stage("old")
	if _, err = r.ReservePublication(ctx, "publication:blocked"+corpus, "", corpus, "snapshot:blocked"+corpus, ""); err == nil {
		t.Fatal("second open publication was not excluded by database")
	}
	if _, err = r.pool.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence+1 WHERE corpus_id=$1`, corpus); err != nil {
		t.Fatal(err)
	}
	if err = r.CommitPublication(ctx, old); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("superseded publisher activated: %v", err)
	}
	if _, err = r.pool.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence-1 WHERE corpus_id=$1`, corpus); err != nil {
		t.Fatal(err)
	}
	current := old
	if err = r.CommitPublication(ctx, current); err != nil {
		t.Fatal(err)
	}
	manifest, err := r.LoadPublicationManifest(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ReservePublication(ctx, "publication:next"+corpus, "", corpus, "snapshot:next"+corpus, manifest.SnapshotRef.SnapshotId)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.CommitPublication(ctx, current); err != nil {
		t.Fatalf("historical committed replay: %v", err)
	}
}
