// Loads exact-byte-pinned graph query configuration before opening backends.
// Corpus, trusted route, namespace policy and traversal budgets are operator
// choices; credentials remain in environment variables and outside fingerprints.
// No I/O runs during package initialization. This validates configuration, not
// backend readiness, linking quality or required benchmark acceptance.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/graph"
	"regulagraph.local/server/internal/retrieval/query"
)

type QueryGraphConfig struct {
	Corpus, Endpoint, Database, FileHash string
	Linking                              query.EntityLinkingPolicy
	Traversal                            graph.TraversalConfig
}

type queryGraphFile struct {
	SchemaVersion int    `json:"schema_version"`
	Corpus        string `json:"corpus"`
	Endpoint      string `json:"endpoint"`
	Database      string `json:"database"`
	Linking       struct {
		Namespaces []struct {
			EntityType string `json:"entity_type"`
			Scope      string `json:"scope"`
		} `json:"namespaces"`
		MaximumQueryBytes       int `json:"maximum_query_bytes"`
		MaximumPhraseTokens     int `json:"maximum_phrase_tokens"`
		MaximumPhrases          int `json:"maximum_phrases"`
		MaximumLookups          int `json:"maximum_lookups"`
		MaximumAliasesPerLookup int `json:"maximum_aliases_per_lookup"`
		MaximumSeeds            int `json:"maximum_seeds"`
	} `json:"linking"`
	Traversal struct {
		MaximumHops  int    `json:"maximum_hops"`
		MaximumPaths int    `json:"maximum_paths"`
		Assertions   int    `json:"assertions"`
		Supports     int    `json:"supports"`
		Bytes        uint64 `json:"bytes"`
	} `json:"traversal"`
}

func LoadQueryGraph(path, expectedSHA256, corpus string) (*QueryGraphConfig, error) {
	if path == "" || !lowerSHA256.MatchString(expectedSHA256) || !candidatePolicyCorpusIDPattern.MatchString(corpus) {
		return nil, errors.New("query graph path/hash/corpus required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("query graph config unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 64<<10 {
		return nil, errors.New("query graph config must be a regular file of 1..65536 bytes")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return nil, errors.New("query graph config read exceeds budget")
	}
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != expectedSHA256 {
		return nil, errors.New("query graph config hash mismatch")
	}
	if err = rejectDuplicateCandidateJSONKeys(raw); err != nil {
		return nil, err
	}
	root, err := candidatePolicyObject(raw, []string{"schema_version", "corpus", "endpoint", "database", "linking", "traversal"}, nil)
	if err != nil {
		return nil, err
	}
	link, err := candidatePolicyObject(root["linking"], []string{"namespaces", "maximum_query_bytes", "maximum_phrase_tokens", "maximum_phrases", "maximum_lookups", "maximum_aliases_per_lookup", "maximum_seeds"}, nil)
	if err != nil {
		return nil, err
	}
	var namespaces []json.RawMessage
	if err = json.Unmarshal(link["namespaces"], &namespaces); err != nil || len(namespaces) == 0 || len(namespaces) > 64 {
		return nil, errors.New("bounded query namespaces required")
	}
	for _, n := range namespaces {
		if _, err = candidatePolicyObject(n, []string{"entity_type", "scope"}, nil); err != nil {
			return nil, err
		}
	}
	if _, err = candidatePolicyObject(root["traversal"], []string{"maximum_hops", "maximum_paths", "assertions", "supports", "bytes"}, nil); err != nil {
		return nil, err
	}
	var data queryGraphFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&data); err != nil {
		return nil, err
	}
	if data.SchemaVersion != 1 || data.Corpus != corpus || data.Database == "" || strings.TrimSpace(data.Database) != data.Database || strings.ContainsRune(data.Database, 0) || len(data.Database) > 128 {
		return nil, errors.New("query graph schema/corpus/database mismatch")
	}
	if err = domain.ValidateGraphEndpoint(data.Endpoint); err != nil {
		return nil, err
	}
	out := &QueryGraphConfig{Corpus: corpus, Endpoint: data.Endpoint, Database: data.Database, FileHash: expectedSHA256,
		Linking:   query.EntityLinkingPolicy{MaximumQueryBytes: data.Linking.MaximumQueryBytes, MaximumPhraseTokens: data.Linking.MaximumPhraseTokens, MaximumPhrases: data.Linking.MaximumPhrases, MaximumLookups: data.Linking.MaximumLookups, MaximumAliasesPerLookup: data.Linking.MaximumAliasesPerLookup, MaximumSeeds: data.Linking.MaximumSeeds},
		Traversal: graph.TraversalConfig{MaximumHops: data.Traversal.MaximumHops, MaximumPaths: data.Traversal.MaximumPaths, Read: domain.GraphReadLimits{Assertions: data.Traversal.Assertions, Supports: data.Traversal.Supports, Bytes: data.Traversal.Bytes}}}
	for _, n := range data.Linking.Namespaces {
		out.Linking.Namespaces = append(out.Linking.Namespaces, query.EntityNamespace{EntityType: n.EntityType, Scope: n.Scope})
	}
	if _, err = out.Linking.Fingerprint(); err != nil {
		return nil, err
	}
	if err = out.Traversal.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}
