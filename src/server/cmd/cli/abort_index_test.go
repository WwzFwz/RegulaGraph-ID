// Tests operator recovery admission, corpus isolation, cancellation and redacted
// failures. Store fakes exercise CLI wiring only; actual snapshot/compensation
// invariants are covered by PostgreSQL integration tests and the operational run.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/indexing"
)

type abortStoreFake struct {
	indexing.PublicationStore
	corpus            string
	loadErr, abortErr error
	aborted           string
}

func (s *abortStoreFake) LoadIndexJobInventory(context.Context, string) (domain.IndexJobInventory, error) {
	return domain.IndexJobInventory{Snapshot: &pb.SnapshotRef{CorpusId: s.corpus}}, s.loadErr
}
func (s *abortStoreFake) AbortPublication(_ context.Context, id string) error {
	s.aborted = id
	return s.abortErr
}

func TestAbortIndexOwnershipAndStoreErrors(t *testing.T) {
	for _, scenario := range []string{"success", "foreign", "missing", "compensation"} {
		t.Run(scenario, func(t *testing.T) {
			s := &abortStoreFake{corpus: "corpus:test"}
			if scenario == "foreign" {
				s.corpus = "corpus:other"
			}
			if scenario == "missing" {
				s.loadErr = errors.New("missing")
			}
			if scenario == "compensation" {
				s.abortErr = errors.New("uncompensated")
			}
			err := abortOwnedIndex(context.Background(), s, abortIndexOptions{corpus: "corpus:test", publication: "publication:test"})
			if (err == nil) != (scenario == "success") {
				t.Fatal("unexpected result", err)
			}
			if (scenario == "foreign" || scenario == "missing") && s.aborted != "" {
				t.Fatal("unauthorized abort")
			}
			if scenario == "success" && s.aborted != "publication:test" {
				t.Fatal("wrong publication")
			}
		})
	}
}

func TestAbortIndexCommand(t *testing.T) {
	for _, scenario := range []string{"success", "error", "cancelled", "invalid", "output"} {
		t.Run(scenario, func(t *testing.T) {
			args := []string{"-corpus", "corpus:test", "-publication", "publication:test"}
			if scenario == "invalid" {
				args = append(args, "-timeout", "0s")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			var out, errOut bytes.Buffer
			var writer io.Writer = &out
			if scenario == "output" {
				writer = publicationBrokenWriter{}
			}
			calls := 0
			code := runAbortIndexWith(ctx, args, writer, &errOut, func(string) string { return "postgres://secret" }, func(ctx context.Context, opts abortIndexOptions) error {
				calls++
				if _, ok := ctx.Deadline(); !ok || opts.corpus != "corpus:test" {
					t.Fatal("missing admission")
				}
				if scenario == "error" {
					return errors.New("postgres://secret")
				}
				return nil
			})
			want := 1
			if scenario == "success" {
				want = 0
			}
			if scenario == "invalid" {
				want = 2
			}
			if code != want || strings.Contains(errOut.String(), "secret") {
				t.Fatal(code, errOut.String())
			}
			if (scenario == "cancelled" || scenario == "invalid") && calls != 0 {
				t.Fatal("invalid dispatch")
			}
			if scenario == "success" && !strings.Contains(out.String(), `"status":"aborted"`) {
				t.Fatal("missing success")
			}
			if scenario != "success" && out.Len() != 0 {
				t.Fatal("false success")
			}
		})
	}
}
