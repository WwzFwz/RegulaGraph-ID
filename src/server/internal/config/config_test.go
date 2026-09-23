// Checks exact-byte ontology pinning at startup, including tampered configuration.
// Fixture verification does not measure model quality or runtime performance.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOntologyRejectsChangedBytes(t *testing.T) {
	raw, err := os.ReadFile("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	expected := hex.EncodeToString(digest[:])
	path := filepath.Join(t.TempDir(), "ontology.jsonc")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ontology, err := LoadOntology(path, expected)
	if err != nil || ontology.ContentHash().Sha256 != expected {
		t.Fatalf("valid ontology was rejected: ontology=%v err=%v", ontology, err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOntology(path, expected); err == nil {
		t.Fatal("changed ontology bytes retained old pin")
	}
}
