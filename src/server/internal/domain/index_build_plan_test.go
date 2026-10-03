// Adversarial tests consume the actual Rust CHUNK-to-INDEX output, authenticate
// its plan/source bytes and check Go admission plus checksum parity. Set
// REGULAGRAPH_INDEX_FIXTURE_DIR to the Rust integration export directory; skipped
// without those artifacts. Synthetic vectors do not prove model quality.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestRustIndexBuildAdmission(t *testing.T) {
	directory := os.Getenv("REGULAGRAPH_INDEX_FIXTURE_DIR")
	if directory == "" {
		t.Skip("Rust INDEX fixture export not configured")
	}
	limits := DefaultWireLimits
	limits.MaxItems = 1_000_000
	read := func(name string, value proto.Message) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = DecodeWire(raw, value, limits); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	plan, batch, source, ref := new(pb.IndexBuildPlan), new(pb.IndexBatch), new(pb.DocumentBatch), new(pb.ArtifactRef)
	read("plan-ref.pb", ref)
	rawPlan := read("plan.pb", plan)
	rawSource := read("source.pb", source)
	read("batch.pb", batch)
	verify := func(raw []byte, ref *pb.ArtifactRef) {
		t.Helper()
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != ref.ContentHash.Sha256 || uint64(len(raw)) != ref.ByteSize {
			t.Fatal("fixture content hash/size mismatch")
		}
	}
	verify(rawPlan, ref)
	verify(rawSource, plan.DocumentBatch)
	if err := ValidatePlannedIndexBatch(batch, plan, ref, source); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*pb.IndexBatch){
		"vector altered": func(b *pb.IndexBatch) {
			b.Records[0].DenseVector.Values[0] = 0.8
			b.Records[0].DenseVector.Values[1] = 0.6
		},
		"sparse weight altered": func(b *pb.IndexBatch) { b.Records[0].SparseVector.Values[0] += 0.5 },
		"source filter forged":  func(b *pb.IndexBatch) { b.Records[0].FilterMetadata.ProvisionFilters[0].SourceBlobId = "blob:forged" },
		"interval forged": func(b *pb.IndexBatch) {
			b.Records[0].FilterMetadata.ProvisionFilters[0].LegalInterval.Start.SupportRefs = []string{"support:forged"}
		},
		"wrong target":     func(b *pb.IndexBatch) { b.Context.SnapshotRef.Sequence++ },
		"wrong identity":   func(b *pb.IndexBatch) { b.Records[0].Meta.RecordId = "index:other" },
		"missing record":   func(b *pb.IndexBatch) { b.Records = b.Records[:1]; b.Counts.Expected = 1; b.Counts.Accepted = 1 },
		"plan substituted": func(b *pb.IndexBatch) { b.BuildPlan.ArtifactId = "plan:forged" },
		"dependency lost":  func(b *pb.IndexBatch) { b.Records[0].Dependencies.Dependencies = nil },
		"producer drift":   func(b *pb.IndexBatch) { b.Dependencies.ProducerManifest.Build = "other" },
		"closure added": func(b *pb.IndexBatch) {
			b.Closures = append(b.Closures, &pb.VisibilityClosure{RecordId: "old:record", ExpectedFromSeq: 1, ToSeq: 7})
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := proto.Clone(batch).(*pb.IndexBatch)
			mutate(changed)
			if err := ValidatePlannedIndexBatch(changed, plan, ref, source); err == nil {
				t.Fatal("invalid Rust output admitted")
			}
		})
	}
	// Even recomputing the vector commitment cannot authorize forged legal facts.
	forged := proto.Clone(batch).(*pb.IndexBatch)
	mutations["source filter forged"](forged)
	forged.OperationsChecksum.Sha256, _ = IndexPlanOperationsChecksum(forged)
	if err := ValidatePlannedIndexBatch(forged, plan, ref, source); err == nil {
		t.Fatal("checksum bypassed source verification")
	}
	for _, mutation := range []func(*pb.IndexBuildPlan){
		func(p *pb.IndexBuildPlan) { p.DocumentBatch.ByteSize = 16<<20 + 1 },
		func(p *pb.IndexBuildPlan) { p.Generation.LexicalAnalyzer.ByteSize = 16<<20 + 1 },
		func(p *pb.IndexBuildPlan) {
			p.DictionaryChain = nil
			for i := 0; i < 5; i++ {
				ref := proto.Clone(p.Generation.LexicalDictionary).(*pb.ArtifactRef)
				ref.ByteSize = 16 << 20
				p.DictionaryChain = append(p.DictionaryChain, ref)
			}
			p.Generation.LexicalDictionary = proto.Clone(p.DictionaryChain[4]).(*pb.ArtifactRef)
		},
		func(p *pb.IndexBuildPlan) { p.Items = append(p.Items, p.Items[0]) },
		func(p *pb.IndexBuildPlan) { p.DictionaryChain = nil },
		func(p *pb.IndexBuildPlan) { p.Generation.DenseManifest.Task = pb.ModelTask_MODEL_TASK_RERANK },
		func(p *pb.IndexBuildPlan) { p.SourceSnapshot.Sequence++ },
		func(p *pb.IndexBuildPlan) { p.LexicalInputPolicy = "other" },
	} {
		p := proto.Clone(plan).(*pb.IndexBuildPlan)
		mutation(p)
		if ValidateIndexBuildPlan(p) == nil {
			t.Fatal("invalid plan accepted")
		}
	}
}
