// Loads real immutable artifact files and checks corruption, budget, generation
// pinning, and frozen-statistics ancestry before query encoding. Shared fixtures
// are reproduced byte-for-byte by the Rust producer; no quality claim follows.
package query

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

type artifactFixtureReader struct {
	raw   map[string][]byte
	calls int
}

func (r *artifactFixtureReader) ReadVerified(_ context.Context, ref *pb.ArtifactRef, maximum uint64) ([]byte, error) {
	r.calls++
	return append([]byte(nil), r.raw[ref.ArtifactId]...), nil
}
func fixtureRef(id string, raw []byte, msg proto.Message) *pb.ArtifactRef {
	h := sha256.Sum256(raw)
	return &pb.ArtifactRef{ArtifactId: id, SchemaVersion: 1, ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: hex.EncodeToString(h[:])}, StorageKey: "objects/" + hex.EncodeToString(h[:]), MediaType: "application/x-protobuf; message=" + string(msg.ProtoReflect().Descriptor().FullName())}
}
func artifactGeneration(t *testing.T) (*pb.IndexGeneration, *artifactFixtureReader) {
	t.Helper()
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	g := &pb.IndexGeneration{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "generation:one"}, DenseManifest: &pb.ModelManifest{ModelId: "model:one", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EMBED, Dimensions: proto.Uint32(2), MaxTokens: 512, Precision: "fp32", Backend: "fixture", Normalization: "l2"}, OntologyVersion: "ontology:v1", FilterFormat: pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1, EmbeddingInputPolicy: "structure-labels-v1"}
	r := &artifactFixtureReader{raw: map[string][]byte{}}
	for _, item := range []struct {
		name string
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
		raw, err := os.ReadFile("../../../../../tests/fixtures/" + item.name)
		if err != nil {
			t.Fatal(err)
		}
		if err = proto.Unmarshal(raw, item.msg); err != nil {
			t.Fatal(err)
		}
		id := item.msg.GetMeta().RecordId
		r.raw[id] = raw
		*item.dest = fixtureRef(id, raw, item.msg)
	}
	return g, r
}

func TestArtifactEncoderReadsImmutableStorageAndPinsGeneration(t *testing.T) {
	g, r := artifactGeneration(t)
	store, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, ref := range []*pb.ArtifactRef{g.LexicalAnalyzer, g.LexicalDictionary, g.LexicalStatistics} {
		if _, err = store.Put(context.Background(), ref, bytes.NewReader(r.raw[ref.ArtifactId])); err != nil {
			t.Fatal(err)
		}
	}
	encoder, err := LoadArtifactBM25Encoder(context.Background(), store, g, nil, nil, 1<<20, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	v, err := encoder.Encode("izin izin pasal unknown")
	if err != nil || v.OOV != 1 || len(v.Values) != 2 {
		t.Fatal(v, err)
	}
	if math.Abs(float64(v.Values[0])-2*math.Log(1+1.5/2.5)) > 1e-6 || math.Abs(float64(v.Values[1])-math.Log(1+2.5/1.5)) > 1e-6 {
		t.Fatal("frozen score mismatch", v)
	}
	g.Meta.RecordId = "mutated"
	bound, population := encoder.ArtifactBinding()
	if bound.Meta.RecordId != "generation:one" || population.Sequence != 7 {
		t.Fatal("lost artifact pins")
	}
	bound.Meta.RecordId = "mutated-copy"
	again, _ := encoder.ArtifactBinding()
	if again.Meta.RecordId != "generation:one" {
		t.Fatal("mutable binding")
	}
}

func TestArtifactEncoderRejectsTamperingBeforeEncoding(t *testing.T) {
	for _, mode := range []string{"hash", "size", "media", "corpus", "id", "analyzer", "budget", "cancel", "dictionary", "nil base"} {
		t.Run(mode, func(t *testing.T) {
			g, r := artifactGeneration(t)
			maximum := uint64(1 << 20)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "hash":
				r.raw[g.LexicalStatistics.ArtifactId][0] ^= 1
			case "size":
				g.LexicalStatistics.ByteSize++
			case "media":
				g.LexicalStatistics.MediaType = g.LexicalDictionary.MediaType
			case "corpus":
				g.Meta.CorpusId = "other"
			case "budget":
				maximum = 1
			case "cancel":
				cancel()
			case "dictionary":
				g.LexicalDictionary = g.LexicalStatistics
			case "id":
				old := g.LexicalAnalyzer.ArtifactId
				g.LexicalAnalyzer.ArtifactId = "other"
				r.raw["other"] = r.raw[old]
			case "analyzer":
				a := new(pb.LexicalAnalyzerArtifact)
				_ = proto.Unmarshal(r.raw[g.LexicalAnalyzer.ArtifactId], a)
				a.UnicodeVersion = "16.0.0"
				raw, _ := proto.Marshal(a)
				g.LexicalAnalyzer = fixtureRef(a.Meta.RecordId, raw, a)
				r.raw[a.Meta.RecordId] = raw
			case "nil base":
				s := new(pb.LexicalStatisticsArtifact)
				_ = proto.Unmarshal(r.raw[g.LexicalStatistics.ArtifactId], s)
				s.DictionaryRegistryRevision = 1
				raw, _ := proto.Marshal(s)
				g.LexicalStatistics = fixtureRef(s.Meta.RecordId, raw, s)
				r.raw[s.Meta.RecordId] = raw
			}
			if _, err := LoadArtifactBM25Encoder(ctx, r, g, nil, nil, maximum, domain.DefaultWireLimits); err == nil {
				t.Fatal("tampered generation accepted")
			}
			if (mode == "cancel" || mode == "budget") && r.calls != 0 {
				t.Fatal("invalid preflight performed reads")
			}
		})
	}
}

func TestArtifactEncoderAllowsOnlyCheckedFrozenBaseAncestry(t *testing.T) {
	g, r := artifactGeneration(t)
	root := new(pb.LexicalDictionaryArtifact)
	_ = proto.Unmarshal(r.raw[g.LexicalDictionary.ArtifactId], root)
	base, err := domain.CheckLexicalDictionaryArtifact(root, "corpus:one", nil, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	child, err := domain.BuildLexicalDictionaryArtifact(&pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "dictionary:3"}, LexicalAnalyzerVersion, 3, []domain.LexicalTerm{{Term: "baru", ID: 3}, {Term: "izin", ID: 1}, {Term: "pasal", ID: 2}}, base, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := proto.Marshal(child)
	g.LexicalDictionary = fixtureRef(child.Meta.RecordId, raw, child)
	r.raw[child.Meta.RecordId] = raw
	encoder, err := LoadArtifactBM25Encoder(context.Background(), r, g, base, base, 1<<20, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	v, err := encoder.Encode("baru")
	if err != nil || len(v.Values) != 1 || math.Abs(float64(v.Values[0])-math.Log(8)) > 1e-6 {
		t.Fatal("new term did not use frozen DF zero", v, err)
	}
	s := new(pb.LexicalStatisticsArtifact)
	_ = proto.Unmarshal(r.raw[g.LexicalStatistics.ArtifactId], s)
	s.DocumentFrequencies[1].TermId = 3
	raw, _ = proto.Marshal(s)
	g.LexicalStatistics = fixtureRef(s.Meta.RecordId, raw, s)
	r.raw[s.Meta.RecordId] = raw
	if _, err = LoadArtifactBM25Encoder(context.Background(), r, g, base, base, 1<<20, domain.DefaultWireLimits); err == nil {
		t.Fatal("future-only term smuggled into frozen population")
	}
}
