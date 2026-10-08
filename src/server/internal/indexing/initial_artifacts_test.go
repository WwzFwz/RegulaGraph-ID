// Exercises artifact admission independently of external stores. Worker batch
// addresses may differ from logical IDs; coordinator-owned typed references must
// retain their exact logical identity. Registered refs, bytes, corpus, type and
// read budgets stay mandatory. Fixtures do not establish model or latency quality.
package indexing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type artifactTestAuthority struct {
	ref *pb.ArtifactRef
}

func (a artifactTestAuthority) LoadArtifact(_ context.Context, _, id string) (*pb.ArtifactRef, error) {
	if a.ref == nil || a.ref.ArtifactId != id {
		return nil, errors.New("not registered")
	}
	return proto.Clone(a.ref).(*pb.ArtifactRef), nil
}
func (artifactTestAuthority) VerifyIndexSourceCheckpoint(context.Context, string, string, *pb.ArtifactRef) error {
	return errors.New("unexpected checkpoint lookup in byte-loader test")
}
func (artifactTestAuthority) VerifyIndexDictionary(context.Context, string, *pb.LexicalDictionaryArtifact) error {
	return errors.New("unexpected dictionary lookup in byte-loader test")
}

func TestInitialArtifactIdentityAdmission(t *testing.T) {
	for _, fixture := range []struct {
		name string
		file string
		msg  interface {
			proto.Message
			GetMeta() *pb.RecordMeta
		}
		physicalAllowed bool
	}{
		{"worker document", "index-source-v1.pb", new(pb.DocumentBatch), true},
		{"typed analyzer", "lexical-analyzer-artifact-v1.pb", new(pb.LexicalAnalyzerArtifact), false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			raw, err := os.ReadFile("../../../../tests/fixtures/" + fixture.file)
			if err != nil {
				t.Fatal(err)
			}
			if err = proto.Unmarshal(raw, fixture.msg); err != nil {
				t.Fatal(err)
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(raw))
			original := &pb.ArtifactRef{ArtifactId: fixture.msg.GetMeta().RecordId,
				ContentHash: &pb.ContentHash{Sha256: sum}, StorageKey: "objects/" + sum,
				MediaType: "application/x-protobuf; message=" + string(fixture.msg.ProtoReflect().Descriptor().FullName()),
				ByteSize:  uint64(len(raw)), SchemaVersion: 1}
			for _, scenario := range []string{"logical", "physical", "worker media", "wrong corpus", "wrong media", "corrupt bytes", "unregistered", "insufficient budget", "reference drift"} {
				t.Run(scenario, func(t *testing.T) {
					ref := proto.Clone(original).(*pb.ArtifactRef)
					if scenario == "physical" {
						ref.ArtifactId = "artifact:document-batch:" + sum
					}
					if scenario == "worker media" {
						ref.MediaType = domain.DocumentBatchMediaType
					}
					corpus := fixture.msg.GetMeta().CorpusId
					if scenario == "wrong corpus" {
						corpus = "corpus:foreign"
					}
					if scenario == "wrong media" {
						ref.MediaType = "application/octet-stream"
					}
					authority := artifactTestAuthority{ref}
					if scenario == "unregistered" {
						authority.ref = nil
					}
					bytes := append([]byte(nil), raw...)
					if scenario == "corrupt bytes" {
						bytes[len(bytes)-1] ^= 1
					}
					budget := uint64(len(raw))
					if scenario == "insufficient budget" {
						budget--
					}
					loader := &initialArtifactLoader{authority: authority,
						reader: indexMemoryArtifacts{ref.ArtifactId: bytes}, corpus: corpus,
						remaining: budget, cache: map[string]initialArtifact{}}
					if scenario == "reference drift" {
						saved := proto.Clone(ref).(*pb.ArtifactRef)
						saved.StorageKey = "objects/other"
						loader.cache[ref.ArtifactId] = initialArtifact{ref: saved, raw: raw}
					}
					decoded := fixture.msg.ProtoReflect().New().Interface()
					err := loader.read(context.Background(), ref, decoded)
					wantOK := scenario == "logical" || (scenario == "physical" || scenario == "worker media") && fixture.physicalAllowed
					if (err == nil) != wantOK {
						t.Fatalf("accepted=%v want=%v: %v", err == nil, wantOK, err)
					}
					if wantOK {
						if !proto.Equal(decoded, fixture.msg) || loader.remaining != 0 {
							t.Fatal("logical payload changed or unique bytes not accounted")
						}
						if err = loader.read(context.Background(), ref, decoded); err != nil {
							t.Fatal("cached artifact consumed budget twice", err)
						}
					}
				})
			}
		})
	}
}
