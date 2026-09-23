// Loads explicit runtime configuration that must be shared across Go and Rust boundaries.
//
// LoadOntology reads at most 1 MiB once at startup, compiles a versioned vocabulary, and checks
// the exact source SHA-256. Callers then pass the immutable object to gateways and workflows;
// package import performs no I/O. Other ingestion/retrieval configuration remains staged.
// Measure startup validation and rejected configuration drift; benchmark thresholds remain
// REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.

package config

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"regulagraph.local/server/internal/domain"
)

const maximumOntologyFileBytes = 1 << 20

var lowerSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadOntology verifies exact source bytes and compiles a shared, immutable ontology once at startup.
// Its bounded read prevents an accidentally large configuration from exhausting runtime memory.
func LoadOntology(path, expectedSHA256 string) (*domain.Ontology, error) {
	if path == "" || !lowerSHA256.MatchString(expectedSHA256) {
		return nil, fmt.Errorf("ontology path and lowercase SHA-256 are required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ontology: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumOntologyFileBytes {
		return nil, fmt.Errorf("ontology must be a regular file of 1..%d bytes", maximumOntologyFileBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumOntologyFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read ontology: %w", err)
	}
	ontology, err := domain.ParseOntologyJSONC(raw)
	if err != nil {
		return nil, err
	}
	if ontology.ContentHash().Sha256 != expectedSHA256 {
		return nil, fmt.Errorf("ontology bytes differ from pinned SHA-256")
	}
	return ontology, nil
}
