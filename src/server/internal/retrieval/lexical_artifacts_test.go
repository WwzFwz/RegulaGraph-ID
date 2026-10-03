// Exercises the artifact-authenticated lexical factory and its snapshot guard.
// Shared serialized fixtures cross Rust/Go; backend doubles cover admission only.
package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/query"
)

type lexicalFixtureReader map[string][]byte

func (r lexicalFixtureReader) ReadVerified(_ context.Context, ref *pb.ArtifactRef, _ uint64) ([]byte, error) {
	return append([]byte(nil), r[ref.ArtifactId]...), nil
}
func lexicalArtifacts(t *testing.T, g *pb.IndexGeneration) lexicalFixtureReader {
	t.Helper()
	r := lexicalFixtureReader{}
	for _, item := range []struct {
		file string
		msg  interface {
			proto.Message
			GetMeta() *pb.RecordMeta
		}
		dest **pb.ArtifactRef
	}{
		{"lexical-analyzer-artifact-v1.pb", new(pb.LexicalAnalyzerArtifact), &g.LexicalAnalyzer},
		{"lexical-dictionary-v1.pb", new(pb.LexicalDictionaryArtifact), &g.LexicalDictionary},
		{"lexical-statistics-v1.pb", new(pb.LexicalStatisticsArtifact), &g.LexicalStatistics},
	} {
		raw, err := os.ReadFile("../../../../tests/fixtures/" + item.file)
		if err != nil {
			t.Fatal(err)
		}
		if err = proto.Unmarshal(raw, item.msg); err != nil {
			t.Fatal(err)
		}
		id := item.msg.GetMeta().RecordId
		h := sha256.Sum256(raw)
		r[id] = raw
		*item.dest = &pb.ArtifactRef{ArtifactId: id, SchemaVersion: 1, ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(h[:])}, StorageKey: "fixture/" + item.file, MediaType: "application/x-protobuf; message=" + string(item.msg.ProtoReflect().Descriptor().FullName())}
	}
	return r
}

func TestLexicalFactoryRequiresArtifactsAndCompatiblePopulation(t *testing.T) {
	input, index, _ := searchFixture()
	reader := lexicalArtifacts(t, input.Generation)
	index.binding.Generation = proto.Clone(input.Generation).(*pb.IndexGeneration)
	r, err := LoadLexicalRetriever(context.Background(), reader, input.Generation, index, nil, nil, 1<<20, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	input.Question = "pasal"
	for _, mode := range []string{"older", "same sequence other snapshot", "later"} {
		t.Run(mode, func(t *testing.T) {
			owned := input
			owned.Context = proto.Clone(input.Context).(*pb.RequestContext)
			switch mode {
			case "older":
				owned.Context.SnapshotRef.Sequence = 6
				owned.Scope.SnapshotSeq = 6
			case "same sequence other snapshot":
				owned.Context.SnapshotRef.SnapshotId = "other"
			case "later":
				owned.Context.SnapshotRef.Sequence = 8
				owned.Scope.SnapshotSeq = 8
				owned.Context.SnapshotRef.SnapshotId = "later"
			}
			before := index.calls
			_, err := r.Retrieve(context.Background(), owned)
			if mode == "later" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || index.calls != before {
				t.Fatal("invalid population reached backend", err)
			}
		})
	}
	bound, _ := r.encoder.ArtifactBinding()
	bound.Meta.RecordId = "forged"
	index.binding.Generation = bound
	if _, err = NewLexicalRetriever(r.encoder, bound, index); err == nil {
		t.Fatal("encoder rebound to foreign generation")
	}
	// A public in-memory encoder is useful for math tests, but not branch admission.
	digest, err := domain.FingerprintLexicalDictionary(query.LexicalAnalyzerVersion, "r1", []domain.LexicalTerm{{Term: "pasal", ID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	unbound, err := query.NewPinnedBM25QueryEncoder(query.SparseDictionaryView{AnalyzerID: query.LexicalAnalyzerVersion, Revision: "r1", Terms: map[string]uint32{"pasal": 1}, Lineage: map[string][32]byte{"r1": digest}}, query.FrozenBM25View{AnalyzerID: query.LexicalAnalyzerVersion, DictionaryRevision: "r1", DictionaryFingerprint: digest, DocumentCount: 1, TotalTokens: 1, DFByID: map[uint32]uint64{1: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewLexicalRetriever(unbound, bound, index); err == nil {
		t.Fatal("unbound encoder admitted")
	}
}
