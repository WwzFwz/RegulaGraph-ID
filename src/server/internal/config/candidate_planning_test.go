// Checks that candidate policy files are bounded, exact-byte-pinned, and semantically valid
// before submission. These fixtures prove config admission, not candidate recall or latency.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCandidatePlanningPoliciesPinsBytesAndRejectsInvalidCatalog(t *testing.T) {
	valid := []byte(`{"schema_version":1,"corpora":{"corpus:fixture":{"scopes_by_type":{"organization":["ID:national","ID:regional"],"provision":["ID:national"]},"include_source_regulation_type":{"provision":true},"maximum_mentions":100,"maximum_scopes_per_mention":4,"maximum_total_scopes":400}}}`)
	path := filepath.Join(t.TempDir(), "candidate-policy.json")
	writeAndLoad := func(raw []byte) error {
		t.Helper()
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		_, err := LoadCandidatePlanningPolicies(path, hex.EncodeToString(digest[:]))
		return err
	}
	if err := writeAndLoad(valid); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(valid)
	policies, err := LoadCandidatePlanningPolicies(path, hex.EncodeToString(digest[:]))
	if err != nil || len(policies) != 1 || !policies["corpus:fixture"].IncludeSourceRegulationType["provision"] {
		t.Fatalf("valid policy lost scope settings: %v, %+v", err, policies)
	}
	if err := os.WriteFile(path, append(append([]byte(nil), valid...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCandidatePlanningPolicies(path, hex.EncodeToString(digest[:])); err == nil {
		t.Fatal("changed catalog bytes retained old pin")
	}
	for name, raw := range map[string][]byte{
		"unknown field":        []byte(strings.Replace(string(valid), `"maximum_total_scopes":400`, `"maximum_total_scopes":400,"misspelled":1`, 1)),
		"case variant":         []byte(strings.Replace(string(valid), `"maximum_mentions":100`, `"Maximum_Mentions":100`, 1)),
		"duplicate field":      []byte(strings.Replace(string(valid), `"maximum_mentions":100`, `"maximum_mentions":100,"maximum_mentions":1`, 1)),
		"duplicate scope":      []byte(strings.Replace(string(valid), `"organization":["ID:national","ID:regional"]`, `"organization":["ID:national"],"organization":["ID:regional"]`, 1)),
		"trailing value":       append(append([]byte(nil), valid...), []byte(` {}`)...),
		"missing type scope":   []byte(strings.Replace(string(valid), `"provision":["ID:national"]`, `"provision":[]`, 1)),
		"unknown source type":  []byte(strings.Replace(string(valid), `"provision":true`, `"unknown":false`, 1)),
		"null source flag":     []byte(strings.Replace(string(valid), `"provision":true`, `"provision":null`, 1)),
		"invalid corpus":       []byte(strings.Replace(string(valid), "corpus:fixture", "corpus with space", 1)),
		"invalid version":      []byte(strings.Replace(string(valid), `"schema_version":1`, `"schema_version":2`, 1)),
		"zero budget":          []byte(strings.Replace(string(valid), `"maximum_mentions":100`, `"maximum_mentions":0`, 1)),
		"impossible scope cap": []byte(strings.Replace(string(valid), `"maximum_scopes_per_mention":4`, `"maximum_scopes_per_mention":1`, 1)),
		"huge positive cap":    []byte(strings.Replace(string(valid), `"maximum_total_scopes":400`, `"maximum_total_scopes":100001`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := writeAndLoad(raw); err == nil {
				t.Fatal("invalid candidate policy catalog accepted")
			}
		})
	}
	if err := writeAndLoad(bytesWithSize(maximumCandidatePolicyFileBytes + 1)); err == nil {
		t.Fatal("oversized candidate policy catalog accepted")
	}
}

func bytesWithSize(size int) []byte { return []byte(strings.Repeat("x", size)) }
