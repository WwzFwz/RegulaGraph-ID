// Tests graph serving config hash, exact JSON shape, route/corpus scope and
// resource limits before backend I/O. Synthetic config is not deployment or
// evidence of graph/model quality or latency acceptance.
package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const graphConfigFixture = `{"schema_version":1,"corpus":"corpus:test","endpoint":"bolt://127.0.0.1:7687","database":"neo4j","linking":{"namespaces":[{"entity_type":"organization","scope":"national"}],"maximum_query_bytes":4096,"maximum_phrase_tokens":4,"maximum_phrases":128,"maximum_lookups":128,"maximum_aliases_per_lookup":16,"maximum_seeds":64},"traversal":{"maximum_hops":3,"maximum_paths":128,"assertions":128,"supports":256,"bytes":1048576}}`

func TestLoadQueryGraphPinnedConfig(t *testing.T) {
	for _, mode := range []string{"valid", "hash", "corpus", "duplicate", "case", "unknown", "remote plaintext", "credentials", "scope", "budget", "trailing", "null", "oversized", "utf8", "database"} {
		t.Run(mode, func(t *testing.T) {
			raw := graphConfigFixture
			corpus := "corpus:test"
			switch mode {
			case "utf8":
				raw = strings.Replace(raw, "national", string([]byte{255}), 1)
			case "database":
				raw = strings.Replace(raw, `"database":"neo4j"`, `"database":" "`, 1)
			case "corpus":
				corpus = "corpus:other"
			case "duplicate":
				raw = strings.Replace(raw, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)
			case "case":
				raw = strings.Replace(raw, `"maximum_hops"`, `"Maximum_Hops"`, 1)
			case "unknown":
				raw = strings.Replace(raw, `"scope":"national"`, `"scope":"national","password":"secret"`, 1)
			case "remote plaintext":
				raw = strings.Replace(raw, "127.0.0.1", "192.0.2.1", 1)
			case "credentials":
				raw = strings.Replace(raw, "127.0.0.1", "user:secret@127.0.0.1", 1)
			case "scope":
				raw = strings.Replace(raw, "national", " national", 1)
			case "budget":
				raw = strings.Replace(raw, `"maximum_seeds":64`, `"maximum_seeds":65`, 1)
			case "trailing":
				raw += "{}"
			case "null":
				raw = strings.Replace(raw, `"linking":{`, `"linking":null,"extra":{`, 1)
			case "oversized":
				raw = strings.Repeat(" ", 65537)
			}
			path := filepath.Join(t.TempDir(), "graph.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
			if mode == "hash" {
				hash = strings.Repeat("0", 64)
			}
			out, err := LoadQueryGraph(path, hash, corpus)
			if mode == "valid" {
				if err != nil || out.Corpus != corpus || out.FileHash != hash || out.Linking.MaximumSeeds != 64 || out.Traversal.Read.Bytes != 1048576 {
					t.Fatal(out, err)
				}
			} else if err == nil {
				t.Fatal("invalid config accepted", mode)
			}
		})
	}
}
