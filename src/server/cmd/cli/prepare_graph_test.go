// Tests operator argument admission, cancellation and redacted failure output
// before storage is opened. Successful scheduling/replay uses the compiled CLI
// in indexing's isolated PostgreSQL/Qdrant/Rust/Neo4j integration test.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"regulagraph.local/server/internal/domain"
)

func TestPrepareGraphCommandAdmission(t *testing.T) {
	path, err := filepath.Abs("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"scope missing", "same snapshot", "bad hash", "timeout", "backend error", "invalid output", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			env := map[string]string{"REGULAGRAPH_POSTGRES_DSN": "postgres://secret", "REGULAGRAPH_ARTIFACTS_DIR": "artifacts", "REGULAGRAPH_ONTOLOGY_PATH": path, "REGULAGRAPH_ONTOLOGY_SHA256": fmt.Sprintf("%x", sha256.Sum256(raw))}
			args := []string{"-corpus", "corpus:test", "-publication", "publication:test", "-snapshot", "snapshot:target", "-base-snapshot", "snapshot:base", "-auth-scope", "scope:test"}
			switch mode {
			case "scope missing":
				args = args[:8]
			case "same snapshot":
				args[7] = args[5]
			case "bad hash":
				env["REGULAGRAPH_ONTOLOGY_SHA256"] = strings.Repeat("0", 64)
			case "timeout":
				args = append(args, "-timeout", "31m")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			calls := 0
			var out, stderr bytes.Buffer
			code := runPrepareGraphWith(ctx, args, &out, &stderr, func(k string) string { return env[k] }, func(ctx context.Context, o prepareGraphOptions) (domain.GraphJobInventory, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok || o.ontology == nil || o.base != "snapshot:base" {
					t.Fatal("lost preparation configuration")
				}
				if mode == "backend error" {
					return domain.GraphJobInventory{}, errors.New("secret")
				}
				return domain.GraphJobInventory{}, nil
			})
			preflight := mode == "scope missing" || mode == "same snapshot" || mode == "bad hash" || mode == "timeout"
			if preflight {
				if calls != 0 || code != 2 {
					t.Fatal("invalid config dispatched", calls, code)
				}
			} else if code != 1 {
				t.Fatal("false scheduling success", code)
			}
			if out.Len() != 0 || strings.Contains(stderr.String(), "secret") {
				t.Fatal("false output or secret disclosure")
			}
		})
	}
}
