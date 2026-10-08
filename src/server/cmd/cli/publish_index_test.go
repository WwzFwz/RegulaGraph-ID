// Verifies operator profile/scope validation, bounded cancellation, truthful
// output and credential redaction before the real publisher is invoked. Actual
// publication/replay/cancellation behavior is tested against PostgreSQL/Qdrant
// by internal/indexing; this fake does not establish backend or model readiness.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestPublishIndexCommand(t *testing.T) {
	env := func(key string) string {
		return map[string]string{"REGULAGRAPH_POSTGRES_DSN": "postgres://secret", "REGULAGRAPH_ARTIFACTS_DIR": "artifacts", "REGULAGRAPH_QDRANT_URL": "http://localhost:6333", "REGULAGRAPH_QDRANT_API_KEY": "secret"}[key]
	}
	args := []string{"-publication", "publication:test", "-corpus", "corpus:test", "-profile", "hybrid"}
	for _, scenario := range []string{"success", "error", "wrong corpus", "nil", "deadline", "unsupported profile", "missing profile", "write failure"} {
		t.Run(scenario, func(t *testing.T) {
			input := append([]string(nil), args...)
			if scenario == "unsupported profile" {
				input[len(input)-1] = "hybrid-graphrag"
			}
			if scenario == "missing profile" {
				input = input[:4]
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "deadline" {
				cancel()
			}
			var out, errOut bytes.Buffer
			calls := 0
			execute := func(ctx context.Context, opts publishIndexOptions) (*pb.SnapshotRef, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok || opts.publication != "publication:test" {
					t.Fatal("missing bounded dispatch")
				}
				if scenario == "error" {
					return nil, errors.New("postgres://secret")
				}
				if scenario == "nil" {
					return nil, nil
				}
				snapshot := &pb.SnapshotRef{CorpusId: "corpus:test", SnapshotId: "snapshot:test", Sequence: 1, RepresentationGeneration: "generation:test", ManifestHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)}}
				if scenario == "wrong corpus" {
					snapshot.CorpusId = "corpus:other"
				}
				return snapshot, nil
			}
			var writer io.Writer = &out
			if scenario == "write failure" {
				writer = publicationBrokenWriter{}
			}
			code := runPublishIndexWith(ctx, input, writer, &errOut, env, execute)
			if scenario == "success" {
				if code != 0 || !strings.Contains(out.String(), `"status":"published"`) {
					t.Fatal(code, errOut.String())
				}
			} else if scenario == "unsupported profile" || scenario == "missing profile" {
				if code != 2 || calls != 0 {
					t.Fatal("invalid profile dispatched", code, calls)
				}
			} else if code != 1 || out.Len() != 0 {
				t.Fatal("false publication success", code)
			}
			if strings.Contains(errOut.String(), "secret") {
				t.Fatal("credentials leaked")
			}
		})
	}
}

type publicationBrokenWriter struct{}

func (publicationBrokenWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }
