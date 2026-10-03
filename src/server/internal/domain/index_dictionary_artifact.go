// Builds and checks typed vocabulary snapshots at the Go/Rust INDEX boundary.
// Mapping checks bind corpus, analyzer, registry revision, parent fingerprint and
// immutable term IDs. Callers still authenticate registry authority and artifact
// bytes; a checked in-memory object is not publication approval. Load once per
// generation with explicit wire budgets. Measure load RSS, p95 and OOV against
// configs/benchmark-targets.yaml; release targets remain REQUIRED_UNMEASURED.
package domain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// CheckedLexicalDictionary owns immutable mapping/lineage copies. Its private
// state can only be constructed through the full snapshot/parent checks below.
type CheckedLexicalDictionary struct {
	corpus      string
	analyzer    string
	revision    uint64
	fingerprint [32]byte
	terms       map[string]uint32
	lineage     map[string][32]byte
}

func (d *CheckedLexicalDictionary) CorpusID() string         { return d.corpus }
func (d *CheckedLexicalDictionary) AnalyzerID() string       { return d.analyzer }
func (d *CheckedLexicalDictionary) RegistryRevision() uint64 { return d.revision }
func (d *CheckedLexicalDictionary) Fingerprint() [32]byte    { return d.fingerprint }
func (d *CheckedLexicalDictionary) Terms() map[string]uint32 {
	out := make(map[string]uint32, len(d.terms))
	for term, id := range d.terms {
		out[term] = id
	}
	return out
}
func (d *CheckedLexicalDictionary) Lineage() map[string][32]byte {
	out := make(map[string][32]byte, len(d.lineage))
	for revision, digest := range d.lineage {
		out[revision] = digest
	}
	return out
}

// CheckLexicalDictionaryArtifact checks local content/ancestry, not database trust.
// A root may start at any positive registry revision; it proves only itself.
func CheckLexicalDictionaryArtifact(a *pb.LexicalDictionaryArtifact, expectedCorpus string,
	parent *CheckedLexicalDictionary, limits WireLimits) (*CheckedLexicalDictionary, error) {
	if a == nil || !validDocumentID(expectedCorpus) || len(a.Entries) > 10_000_000 {
		return nil, errors.New("invalid dictionary artifact or expected corpus")
	}
	if err := ValidateWire(a, limits); err != nil {
		return nil, fmt.Errorf("dictionary wire: %w", err)
	}
	if a.Meta.CorpusId != expectedCorpus || a.Meta.Visibility != nil {
		return nil, errors.New("dictionary corpus or visibility mismatch")
	}
	revision, err := LexicalRevisionName(a.RegistryRevision)
	if err != nil {
		return nil, err
	}
	if (a.ParentRegistryRevision != nil) != (a.ParentMappingFingerprint != nil) ||
		(a.ParentRegistryRevision != nil) != (parent != nil) {
		return nil, errors.New("dictionary requires exactly its declared checked parent")
	}
	if parent != nil && (parent.corpus != expectedCorpus || parent.analyzer != a.AnalyzerId ||
		parent.revision != a.GetParentRegistryRevision() || parent.revision >= a.RegistryRevision ||
		hex.EncodeToString(parent.fingerprint[:]) != a.ParentMappingFingerprint.Sha256 || len(parent.lineage) >= 16_384) {
		return nil, errors.New("dictionary parent corpus, analyzer, revision or digest mismatch")
	}
	entries := make([]LexicalTerm, len(a.Entries))
	for i, entry := range a.Entries {
		if entry == nil {
			return nil, errors.New("nil dictionary entry")
		}
		entries[i] = LexicalTerm{Term: entry.Term, ID: entry.TermId}
	}
	digest, err := fingerprintOrderedLexicalDictionary(a.AnalyzerId, revision, entries)
	if err != nil {
		return nil, err
	}
	if hex.EncodeToString(digest[:]) != a.MappingFingerprint.Sha256 {
		return nil, errors.New("dictionary mapping fingerprint mismatch")
	}
	terms := make(map[string]uint32, len(entries))
	for _, entry := range entries {
		terms[entry.Term] = entry.ID
	}
	lineage := map[string][32]byte{revision: digest}
	if parent != nil {
		for term, id := range parent.terms {
			if terms[term] != id {
				return nil, errors.New("dictionary removes or reassigns parent term")
			}
		}
		for name, ancestor := range parent.lineage {
			lineage[name] = ancestor
		}
	}
	return &CheckedLexicalDictionary{expectedCorpus, a.AnalyzerId, a.RegistryRevision, digest, terms, lineage}, nil
}

// BuildLexicalDictionaryArtifact produces deterministic sorted entries from a
// trusted allocator read. Metadata and registry authority remain caller-owned.
func BuildLexicalDictionaryArtifact(meta *pb.RecordMeta, analyzer string, revision uint64,
	entries []LexicalTerm, parent *CheckedLexicalDictionary, limits WireLimits) (*pb.LexicalDictionaryArtifact, error) {
	if meta == nil || limits.MaxBytes <= 0 || limits.MaxItems <= 0 || limits.MaxDepth <= 0 ||
		len(entries) > 10_000_000 || len(entries) > limits.MaxItems || !validDocumentID(analyzer) {
		return nil, errors.New("invalid bounded dictionary build")
	}
	if err := ValidateWire(meta, limits); err != nil {
		return nil, fmt.Errorf("dictionary metadata: %w", err)
	}
	// Wire accounting charges each repeated message twice, plus metadata and
	// fingerprint messages. Reject before sorting/cloning the full vocabulary.
	overhead := 2
	if parent != nil {
		overhead++
	}
	if meta.Visibility != nil || limits.MaxItems < overhead || len(entries) > (limits.MaxItems-overhead)/2 {
		return nil, errors.New("dictionary visibility or wire item budget invalid")
	}
	name, err := LexicalRevisionName(revision)
	if err != nil {
		return nil, err
	}
	remaining := limits.MaxBytes
	for _, entry := range entries {
		if len(entry.Term) > remaining {
			return nil, errors.New("dictionary text exceeds byte budget")
		}
		remaining -= len(entry.Term)
	}
	ordered := append([]LexicalTerm(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Term < ordered[j].Term })
	digest, err := fingerprintOrderedLexicalDictionary(analyzer, name, ordered)
	if err != nil {
		return nil, err
	}
	a := &pb.LexicalDictionaryArtifact{Meta: proto.Clone(meta).(*pb.RecordMeta), AnalyzerId: analyzer,
		RegistryRevision: revision, MappingFingerprint: &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])},
		Entries: make([]*pb.LexicalDictionaryEntry, len(ordered))}
	for i, entry := range ordered {
		a.Entries[i] = &pb.LexicalDictionaryEntry{Term: entry.Term, TermId: entry.ID}
	}
	if parent != nil {
		a.ParentRegistryRevision = proto.Uint64(parent.revision)
		a.ParentMappingFingerprint = &pb.ContentHash{Sha256: hex.EncodeToString(parent.fingerprint[:])}
	}
	if _, err = CheckLexicalDictionaryArtifact(a, meta.CorpusId, parent, limits); err != nil {
		return nil, err
	}
	return a, nil
}
