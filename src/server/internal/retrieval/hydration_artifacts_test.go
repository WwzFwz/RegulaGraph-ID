// Regression coverage for DocumentBatch-owned normalized bytes without standalone
// registry rows. Exercises source admission, hash/size budgets, descriptor drift,
// and nested-to-root cache isolation; fixtures do not measure model quality.
package retrieval

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type hydrationArtifactCatalog struct {
	EvidenceCatalog
	ref   *pb.ArtifactRef
	calls int
}

func (s *hydrationArtifactCatalog) LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error) {
	s.calls++
	if s.ref == nil {
		return nil, domain.ErrNotFound
	}
	return s.ref, nil
}

type hydrationArtifactReader struct {
	raw   []byte
	calls int
}

func (r *hydrationArtifactReader) ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error) {
	r.calls++
	return r.raw, nil
}

func TestHydrationNestedTextAuthority(t *testing.T) {
	for _, scenario := range []string{"unregistered nested", "missing root after nested", "mismatched root after nested", "unknown document", "unknown text", "corrupt bytes", "byte budget", "descriptor drift"} {
		t.Run(scenario, func(t *testing.T) {
			raw := []byte("Pasal 1: ketentuan.")
			ref := &pb.ArtifactRef{ArtifactId: "text:normalized", StorageKey: "text/normalized.bin", SchemaVersion: 1, ByteSize: uint64(len(raw)), MediaType: "text/plain", ContentHash: &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256(raw))}}
			catalog := &hydrationArtifactCatalog{}
			reader := &hydrationArtifactReader{raw: raw}
			doc := &evidenceDocument{texts: map[string]*pb.TextArtifact{"text:logical": {NormalizedTextRef: ref}}}
			l := &evidenceLoader{catalog: catalog, reader: reader, corpus: "corpus:test", remaining: 1024, cache: map[string]evidenceArtifact{}, documents: map[string]*evidenceDocument{"document:admitted": doc}}
			textID := "text:logical"
			if scenario == "unknown document" {
				l.documents = map[string]*evidenceDocument{}
			}
			if scenario == "unknown text" {
				textID = "text:absent"
			}
			if scenario == "corrupt bytes" {
				reader.raw = []byte("Pasal 2: ketentuan.")
			}
			if scenario == "byte budget" {
				l.remaining = 1
			}
			got, err := l.normalizedText(context.Background(), doc, textID)
			fails := scenario == "unknown document" || scenario == "unknown text" || scenario == "corrupt bytes" || scenario == "byte budget"
			if fails {
				if err == nil {
					t.Fatal("invalid nested read accepted")
				}
				return
			}
			if err != nil || string(got) != string(raw) || catalog.calls != 0 {
				t.Fatal("nested ref incorrectly requires registry", err, catalog.calls)
			}
			switch scenario {
			case "missing root after nested", "mismatched root after nested":
				if scenario == "mismatched root after nested" {
					catalog.ref = proto.Clone(ref).(*pb.ArtifactRef)
					catalog.ref.StorageKey = "text/other.bin"
				}
				if _, err = l.bytes(context.Background(), ref); err == nil || catalog.calls != 1 {
					t.Fatal("nested cache bypassed root registry", err)
				}
			case "descriptor drift":
				changed := proto.Clone(ref).(*pb.ArtifactRef)
				changed.StorageKey = "text/other.bin"
				doc.texts[textID].NormalizedTextRef = changed
				if _, err = l.normalizedText(context.Background(), doc, textID); err == nil {
					t.Fatal("cached descriptor drift accepted")
				}
			default:
				if _, err = l.normalizedText(context.Background(), doc, textID); err != nil || reader.calls != 1 || l.remaining != 1024-uint64(len(raw)) {
					t.Fatal("cache/budget mismatch", err)
				}
			}
		})
	}
}
