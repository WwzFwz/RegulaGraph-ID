// Checks graph CLI admission, hash-pinned policy, native optionality and secret
// redaction without network side effects. Actual executable-to-database wiring
// is exercised by the opt-in native published graph integration test.
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
	"regulagraph.local/server/internal/workflows"
)

func TestGraphQueryCLIConfigurationAndNativeOptionality(t *testing.T) {
	raw := `{"schema_version":1,"corpus":"corpus:test","endpoint":"bolt://127.0.0.1:7687","database":"neo4j","linking":{"namespaces":[{"entity_type":"organization","scope":"national"}],"maximum_query_bytes":4096,"maximum_phrase_tokens":4,"maximum_phrases":128,"maximum_lookups":128,"maximum_aliases_per_lookup":16,"maximum_seeds":64},"traversal":{"maximum_hops":3,"maximum_paths":128,"assertions":128,"supports":256,"bytes":1048576}}`
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"graph", "hybrid-graph", "missing credentials", "wrong hash", "dense with graph config", "hybrid without native", "hybrid overbudget"} {
		t.Run(mode, func(t *testing.T) {
			env := queryEnvironment()
			env["REGULAGRAPH_QUERY_GRAPH_CONFIG"] = path
			env["REGULAGRAPH_QUERY_GRAPH_CONFIG_SHA256"] = fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
			env["REGULAGRAPH_NEO4J_USERNAME"] = "neo4j"
			env["REGULAGRAPH_NEO4J_PASSWORD"] = "secret-password"
			env["REGULAGRAPH_QUERY_NATIVE_ENDPOINT"] = ""
			profile := "graph"
			if strings.HasPrefix(mode, "hybrid") {
				profile = "hybrid-graph"
			}
			if mode == "hybrid-graph" {
				env["REGULAGRAPH_QUERY_NATIVE_ENDPOINT"] = "127.0.0.1:50053"
			}
			if mode == "missing credentials" {
				env["REGULAGRAPH_NEO4J_PASSWORD"] = ""
			}
			if mode == "wrong hash" {
				env["REGULAGRAPH_QUERY_GRAPH_CONFIG_SHA256"] = strings.Repeat("0", 64)
			}
			if mode == "dense with graph config" {
				profile = "vector"
			}
			args := []string{"-question", "Badan A?", "-as-of", "2026-01-01", "-profile", profile}
			if mode == "hybrid overbudget" {
				args = append(args, "-limit", "86")
				env["REGULAGRAPH_QUERY_NATIVE_ENDPOINT"] = "127.0.0.1:50053"
			}
			var out, stderr bytes.Buffer
			calls := 0
			code := runQueryEvidenceWith(context.Background(), args, &out, &stderr, func(k string) string { return env[k] }, func(_ context.Context, o queryOptions, r *pb.QuestionRequest) (*workflows.RAGResult, error) {
				calls++
				if o.graphConfig == nil || o.authScope != "operator:local-query" || o.graphConfig.Linking.MaximumSeeds != 64 {
					t.Fatal("config not admitted")
				}
				return nil, errors.New("secret-password")
			})
			valid := mode == "graph" || mode == "hybrid-graph"
			if valid && (code != 1 || calls != 1) || !valid && (code != 2 || calls != 0) || out.Len() != 0 || strings.Contains(stderr.String(), "secret") {
				t.Fatal(mode, code, calls, stderr.String())
			}
		})
	}
}
