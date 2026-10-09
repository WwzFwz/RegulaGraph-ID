// Checks graph catalog identity, transport and count/output boundaries without
// database or model access. Fixtures exercise validation only, not authority.
package domain

import (
	"math"
	"strings"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestGraphCatalogBoundaries(t *testing.T) {
	hash := strings.Repeat("a", 64)
	valid := func() GraphCatalogBinding {
		b := GraphBinding{CorpusID: "corpus:test", Generation: "generation:graph", PublicationID: "publication:graph", Fence: 1, Sequence: 2, RegistryRevision: 3,
			BaseSnapshot: &pb.SnapshotRef{CorpusId: "corpus:test", SnapshotId: "snapshot:base", Sequence: 1, ManifestHash: &pb.ContentHash{Sha256: hash}, RepresentationGeneration: "generation:index"}}
		digest, err := GraphBindingHash(b)
		if err != nil {
			t.Fatal(err)
		}
		return GraphCatalogBinding{Binding: b, Endpoint: "bolt://127.0.0.1:7687", Database: "neo4j", BindingHash: digest, OperationsHash: hash, InventoryHash: hash, Records: 8, Edges: 5, Operations: 1,
			Outputs: []*pb.ArtifactRef{{ArtifactId: "artifact:graph", ContentHash: &pb.ContentHash{Sha256: hash}, StorageKey: "graph.pb", ByteSize: 30, MediaType: GraphDeltaMediaType, SchemaVersion: 1}}}
	}
	b := valid()
	if err := ValidateGraphCatalogBinding(b); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWire(b.ExpectedBackend(), DefaultWireLimits); err != nil || b.ExpectedBackend().ExpectedCounts.Expected != 13 {
		t.Fatal("expected graph count semantics", err)
	}
	for name, mutate := range map[string]func(*GraphCatalogBinding){
		"changed binding":   func(b *GraphCatalogBinding) { b.Binding.Fence++ },
		"foreign base":      func(b *GraphCatalogBinding) { b.Binding.BaseSnapshot.CorpusId = "corpus:other" },
		"unsafe transport":  func(b *GraphCatalogBinding) { b.Endpoint = "bolt://remote.invalid:7687" },
		"credential in URI": func(b *GraphCatalogBinding) { b.Endpoint = "bolt+s://user:secret@remote.invalid:7687" },
		"count overflow":    func(b *GraphCatalogBinding) { b.Records = math.MaxInt64 },
		"missing output":    func(b *GraphCatalogBinding) { b.Outputs = nil },
		"wrong media":       func(b *GraphCatalogBinding) { b.Outputs[0].MediaType = "application/pdf" },
		"oversized output":  func(b *GraphCatalogBinding) { b.Outputs[0].ByteSize = 17 << 20 },
		"duplicate output":  func(b *GraphCatalogBinding) { b.Outputs = append(b.Outputs, b.Outputs[0]); b.Operations++ },
		"unknown ref":       func(b *GraphCatalogBinding) { b.Outputs[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1}) },
	} {
		t.Run(name, func(t *testing.T) {
			b := valid()
			mutate(&b)
			if err := ValidateGraphCatalogBinding(b); err == nil {
				t.Fatal("invalid graph catalog accepted")
			}
		})
	}
}
