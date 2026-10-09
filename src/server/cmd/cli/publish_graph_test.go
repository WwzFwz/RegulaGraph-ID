// Tests operator graph publication admission and truthful/redacted output.
// Native indexing integration invokes the compiled command for real storage,
// while these cases reject malformed flags before opening any backend.
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

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestPublishGraphCommand(t *testing.T) {
	path, err := filepath.Abs("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "backend error", "wrong corpus", "wrong snapshot", "nil", "scope missing", "bad hash", "bad route", "timeout", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			env := map[string]string{"REGULAGRAPH_POSTGRES_DSN": "postgres://secret", "REGULAGRAPH_ARTIFACTS_DIR": "artifacts", "REGULAGRAPH_NEO4J_URI": "bolt://127.0.0.1:7687", "REGULAGRAPH_NEO4J_DATABASE": "neo4j", "REGULAGRAPH_NEO4J_USERNAME": "neo4j", "REGULAGRAPH_NEO4J_PASSWORD": "secret", "REGULAGRAPH_QDRANT_URL": "http://127.0.0.1:6333", "REGULAGRAPH_ONTOLOGY_PATH": path, "REGULAGRAPH_ONTOLOGY_SHA256": fmt.Sprintf("%x", sha256.Sum256(raw))}
			args := []string{"-corpus", "corpus:test", "-publication", "publication:test", "-snapshot", "snapshot:test", "-generation", "generation:test", "-auth-scope", "scope:test"}
			switch mode {
			case "scope missing":
				args = args[:8]
			case "bad hash":
				env["REGULAGRAPH_ONTOLOGY_SHA256"] = strings.Repeat("0", 64)
			case "bad route":
				env["REGULAGRAPH_NEO4J_URI"] = "bolt://secret@remote:7687"
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
			code := runPublishGraphWith(ctx, args, &out, &stderr, func(k string) string { return env[k] }, func(ctx context.Context, o publishGraphOptions) (*pb.SnapshotRef, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok || o.ontology == nil || o.scope != "scope:test" {
					t.Fatal("lost admission inputs")
				}
				if mode == "backend error" {
					return nil, errors.New("secret")
				}
				if mode == "nil" {
					return nil, nil
				}
				s := &pb.SnapshotRef{CorpusId: o.corpus, SnapshotId: o.snapshot, Sequence: 2, ManifestHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)}, RepresentationGeneration: "generation:test"}
				if mode == "wrong corpus" {
					s.CorpusId = "corpus:other"
				}
				if mode == "wrong snapshot" {
					s.SnapshotId = "snapshot:other"
				}
				return s, nil
			})
			preflight := mode == "scope missing" || mode == "bad hash" || mode == "bad route" || mode == "timeout"
			if preflight {
				if calls != 0 || code != 2 {
					t.Fatal("invalid request dispatched", code, calls)
				}
			} else if mode == "success" {
				if code != 0 || !strings.Contains(out.String(), `"status":"published"`) {
					t.Fatal(code, stderr.String())
				}
			} else if code != 1 || out.Len() != 0 {
				t.Fatal("false success", code)
			}
			if strings.Contains(stderr.String(), "secret") {
				t.Fatal("secret disclosed")
			}
		})
	}
}
