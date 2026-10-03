// Exercises the typed dictionary handoff and adversarial parent/content cases.
// The binary fixture is shared with Rust; these synthetic terms do not establish
// model quality, registry authority, or required latency/throughput benchmarks.
package domain

import (
	"bytes"
	"encoding/hex"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func dictionaryRoot(t *testing.T) (*pb.LexicalDictionaryArtifact, *CheckedLexicalDictionary) {
	t.Helper()
	a, err := BuildLexicalDictionaryArtifact(&pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "dictionary:2"},
		"regulagraph-lexical-nfc-ascii-v1", 2, []LexicalTerm{{Term: "pasal", ID: 2}, {Term: "izin", ID: 1}}, nil, DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := CheckLexicalDictionaryArtifact(a, "corpus:one", nil, DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	return a, checked
}

func TestLexicalDictionaryArtifactFixture(t *testing.T) {
	a, checked := dictionaryRoot(t)
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../../../tests/fixtures/lexical-dictionary-v1.pb")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, fixture) {
		t.Fatal("Go builder differs from shared Rust wire fixture")
	}
	var decoded pb.LexicalDictionaryArtifact
	if err = DecodeWire(raw, &decoded, DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	if _, err = CheckLexicalDictionaryArtifact(&decoded, "corpus:one", nil, DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	a.Entries[0].TermId = 99
	terms := checked.Terms()
	terms["izin"] = 98
	if checked.Terms()["izin"] != 1 {
		t.Fatal("checked mapping aliases caller memory")
	}
}

func TestLexicalDictionaryArtifactParentClosure(t *testing.T) {
	_, parent := dictionaryRoot(t)
	entries := []LexicalTerm{{Term: "izin", ID: 1}, {Term: "pasal", ID: 2}, {Term: "tidak", ID: 3}}
	child, err := BuildLexicalDictionaryArtifact(&pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "dictionary:3"},
		parent.AnalyzerID(), 3, entries, parent, DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := CheckLexicalDictionaryArtifact(child, "corpus:one", parent, DefaultWireLimits)
	if err != nil || checked.Lineage()["lexrev:2"] != parent.Fingerprint() {
		t.Fatalf("missing checked ancestry: %v", err)
	}
	cases := map[string]func(*pb.LexicalDictionaryArtifact){
		"unsorted":              func(a *pb.LexicalDictionaryArtifact) { a.Entries[0], a.Entries[1] = a.Entries[1], a.Entries[0] },
		"foreign corpus":        func(a *pb.LexicalDictionaryArtifact) { a.Meta.CorpusId = "corpus:other" },
		"parent revision":       func(a *pb.LexicalDictionaryArtifact) { a.ParentRegistryRevision = proto.Uint64(1) },
		"missing parent digest": func(a *pb.LexicalDictionaryArtifact) { a.ParentMappingFingerprint = nil },
		"hash": func(a *pb.LexicalDictionaryArtifact) {
			a.MappingFingerprint.Sha256 = child.ParentMappingFingerprint.Sha256
		},
		"reassigned with valid hash": func(a *pb.LexicalDictionaryArtifact) {
			a.Entries[0].TermId = 4
			digest, hashErr := FingerprintLexicalDictionary(a.AnalyzerId, "lexrev:3", []LexicalTerm{{Term: "izin", ID: 4}, {Term: "pasal", ID: 2}, {Term: "tidak", ID: 3}})
			if hashErr != nil {
				t.Fatal(hashErr)
			}
			a.MappingFingerprint.Sha256 = hex.EncodeToString(digest[:])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := proto.Clone(child).(*pb.LexicalDictionaryArtifact)
			mutate(a)
			if _, err := CheckLexicalDictionaryArtifact(a, "corpus:one", parent, DefaultWireLimits); err == nil {
				t.Fatal("accepted corrupt snapshot")
			}
		})
	}
	if _, err = CheckLexicalDictionaryArtifact(child, "corpus:one", nil, DefaultWireLimits); err == nil {
		t.Fatal("accepted unverified parent")
	}
	if _, err = CheckLexicalDictionaryArtifact(child, "corpus:one", parent, WireLimits{MaxBytes: 4, MaxDepth: 64, MaxItems: 100}); err == nil {
		t.Fatal("accepted oversized artifact")
	}
}

func TestLexicalDictionaryArtifactWireItemBudget(t *testing.T) {
	a, parent := dictionaryRoot(t)
	entries := []LexicalTerm{{Term: "izin", ID: 1}, {Term: "pasal", ID: 2}}
	for _, test := range []struct {
		parent   *CheckedLexicalDictionary
		revision uint64
		cost     int
	}{{nil, 2, 6}, {parent, 3, 7}} {
		limits := DefaultWireLimits
		limits.MaxItems = test.cost
		if _, err := BuildLexicalDictionaryArtifact(a.Meta, a.AnalyzerId, test.revision, entries, test.parent, limits); err != nil {
			t.Fatalf("exact wire item budget rejected: %v", err)
		}
		limits.MaxItems--
		if _, err := BuildLexicalDictionaryArtifact(a.Meta, a.AnalyzerId, test.revision, entries, test.parent, limits); err == nil {
			t.Fatal("undersized wire item budget accepted")
		}
	}
}
