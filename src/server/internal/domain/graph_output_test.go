// Admits actual Rust ASSEMBLE artifacts and tests source/visibility/support drift.
// The optional fixture path is produced by the Rust worker test, never by this
// validator. Corpus/registry decisions are synthetic, so PASS proves structural
// cross-language agreement, not extraction quality or live storage authority.
package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestGraphOutputFromRustWorker(t *testing.T) {
	dir := os.Getenv("REGULAGRAPH_GRAPH_FIXTURE_DIR")
	if dir == "" {
		t.Skip("REGULAGRAPH_GRAPH_FIXTURE_DIR requires real Rust worker fixture export")
	}
	read := func(name string, m proto.Message) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = DecodeWire(raw, m, DefaultWireLimits); err != nil {
			t.Fatal(name, err)
		}
	}
	p, d, e, r, v, delta := new(pb.GraphAssemblyPlan), new(pb.DocumentBatch), new(pb.ExtractionBatch), new(pb.ResolutionBatch), new(pb.RegistryEntityView), new(pb.GraphDelta)
	read("plan.pb", p)
	read("document.pb", d)
	read("extraction.pb", e)
	read("resolution.pb", r)
	read("registry.pb", v)
	read("delta.pb", delta)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "configs", "ontology-v1.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseOntologyJSONC(raw)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string][]byte{}
	for i, text := range d.TextArtifacts {
		raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("text-%d.bin", i)))
		if err != nil {
			t.Fatal(err)
		}
		texts[text.Meta.RecordId] = raw
	}
	in := GraphOutputSources{d, e, r, v, texts}
	if err := ValidatePlannedGraphDelta(delta, p, in, o); err != nil {
		t.Fatal("Rust output rejected", err)
	}
	for name, mutate := range map[string]func(*pb.GraphDelta){
		"missing entity":    func(d *pb.GraphDelta) { d.Entities = d.Entities[:1] },
		"missing support":   func(d *pb.GraphDelta) { d.Supports = nil },
		"lost mention":      func(d *pb.GraphDelta) { d.Mentions = d.Mentions[:1] },
		"lost decision":     func(d *pb.GraphDelta) { d.Decisions = nil },
		"future revision":   func(d *pb.GraphDelta) { d.RegistryRevision++ },
		"wrong base":        func(d *pb.GraphDelta) { d.BaseSnapshot.Sequence++ },
		"wrong visibility":  func(d *pb.GraphDelta) { d.Assertions[0].Meta.Visibility.FromSeq++ },
		"altered predicate": func(d *pb.GraphDelta) { d.Assertions[0].PredicateId = "amends" },
		"reverse edge": func(d *pb.GraphDelta) {
			d.Assertions[0].SubjectId, d.Assertions[0].ObjectId = d.Assertions[0].ObjectId, d.Assertions[0].SubjectId
		},
		"invented canonical ID": func(d *pb.GraphDelta) { d.Assertions[0].Meta.RecordId = "assertion:invented" },
		"invented source":       func(d *pb.GraphDelta) { d.Supports[0].SourceRefs[0].SourceBlobId = "source:foreign" },
		"altered quote":         func(d *pb.GraphDelta) { d.Supports[0].EvidenceSpans[0].EndByte++ },
		"lost dependency":       func(d *pb.GraphDelta) { d.Dependencies.Dependencies = d.Dependencies.Dependencies[1:] },
		"lost negative lookup":  func(d *pb.GraphDelta) { d.Dependencies.LookupScopeRevisions = nil },
		"unrequested close": func(d *pb.GraphDelta) {
			d.VisibilityClosures = []*pb.VisibilityClosure{{RecordId: d.Entities[0].Meta.RecordId, ExpectedFromSeq: 1, ToSeq: 2}}
		},
		"false report count":   func(d *pb.GraphDelta) { d.ValidationReport.CheckedRecords++ },
		"unknown nested field": func(d *pb.GraphDelta) { d.Supports[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1}) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := proto.Clone(delta).(*pb.GraphDelta)
			mutate(copy)
			if ValidatePlannedGraphDelta(copy, p, in, o) == nil {
				t.Fatal("forged delta accepted")
			}
		})
	}
	t.Run("source text hash", func(t *testing.T) {
		copy := map[string][]byte{}
		for id, raw := range texts {
			copy[id] = append([]byte(nil), raw...)
			copy[id][0] ^= 1
		}
		bad := in
		bad.NormalizedTexts = copy
		if ValidatePlannedGraphDelta(delta, p, bad, o) == nil {
			t.Fatal("corrupt text accepted")
		}
	})
	t.Run("worker budget", func(t *testing.T) {
		copy := proto.Clone(p).(*pb.GraphAssemblyPlan)
		copy.DocumentBatch.ByteSize = 16 << 20
		if ValidatePlannedGraphDelta(delta, copy, in, o) == nil {
			t.Fatal("worker budget overflow accepted")
		}
	})
	t.Run("future context schema", func(t *testing.T) {
		bad := in
		bad.Document = proto.Clone(d).(*pb.DocumentBatch)
		bad.Extraction = proto.Clone(e).(*pb.ExtractionBatch)
		bad.Resolution = proto.Clone(r).(*pb.ResolutionBatch)
		bad.Document.Context.SchemaVersion = 2
		bad.Extraction.Context.SchemaVersion = 2
		bad.Resolution.Context.SchemaVersion = 2
		if ValidatePlannedGraphDelta(delta, p, bad, o) == nil {
			t.Fatal("unknown source context schema accepted")
		}
	})
	t.Run("text media", func(t *testing.T) {
		bad := in
		bad.Document = proto.Clone(d).(*pb.DocumentBatch)
		bad.Document.TextArtifacts[0].NormalizedTextRef.MediaType = "application/pdf"
		if ValidatePlannedGraphDelta(delta, p, bad, o) == nil {
			t.Fatal("non-text normalized artifact accepted")
		}
	})
	t.Run("empty text item budget", func(t *testing.T) {
		bad := in
		bad.NormalizedTexts = make(map[string][]byte, DefaultWireLimits.MaxItems+1)
		for i := 0; i <= DefaultWireLimits.MaxItems; i++ {
			bad.NormalizedTexts[fmt.Sprintf("text:%d", i)] = nil
		}
		if ValidatePlannedGraphDelta(delta, p, bad, o) == nil {
			t.Fatal("unbounded empty text entries accepted")
		}
	})
}
