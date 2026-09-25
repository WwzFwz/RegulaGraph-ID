// Batch tests cover cross-record accounting, source binding, and closure
// conflicts. They do not authenticate artifacts or prove backend publication.
package domain

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func localIndexBatchFixture() (*pb.IndexBatch, *IndexSourceView) {
	record := indexFilterFixture()
	record.DenseVector = &pb.DenseVector{Values: []float32{0.2, 0.8}, Dimensions: 2, ModelId: "model:one"}
	record.SparseVector = &pb.SparseVector{Indices: []uint32{2}, Values: []float32{1.25}}
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	artifact := &pb.ArtifactRef{ArtifactId: "artifact:one", ContentHash: hash,
		StorageKey: "objects/a", MediaType: "application/x-protobuf", SchemaVersion: 1}
	manifest := &pb.ProducerManifest{Software: "fixture", Build: "fixture", SchemaVersion: 1, ConfigHash: hash}
	generation := &pb.IndexGeneration{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "generation:one"},
		DenseManifest: &pb.ModelManifest{ModelId: "model:one", Version: "1", WeightsHash: hash,
			TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EMBED, Dimensions: proto.Uint32(2),
			MaxTokens: 512, Precision: "fp32", Backend: "fixture"},
		LexicalAnalyzer:   proto.Clone(artifact).(*pb.ArtifactRef),
		LexicalDictionary: proto.Clone(artifact).(*pb.ArtifactRef),
		LexicalStatistics: proto.Clone(artifact).(*pb.ArtifactRef),
		OntologyVersion:   "ontology:v1",
		FilterFormat:      pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1,
	}
	batch := &pb.IndexBatch{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "index-batch:one"},
		Context: &pb.RequestContext{SchemaVersion: 1, RequestId: "request:one", TraceId: "trace:one",
			CorpusId: "corpus:one", Deadline: timestamppb.Now(), ConfigFingerprint: hash, AuthScopeRef: "auth:one"},
		Generation: generation, Records: []*pb.IndexRecord{record},
		Counts: &pb.Counts{Expected: 1, Accepted: 1}, OperationsChecksum: hash,
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:batch", ProducerManifest: manifest},
	}
	view := &IndexSourceView{corpusID: "corpus:one",
		chunks:          map[string]map[string]bool{"chunk:one": {"version:one": true, "version:two": true}},
		chunkVisibility: map[string]*pb.Visibility{},
		versions:        map[string]indexVersionFacts{},
	}
	for _, filter := range record.FilterMetadata.ProvisionFilters {
		view.versions[filter.ProvisionVersionId] = indexVersionFacts{
			regulationID: filter.RegulationId, sourceBlobID: filter.SourceBlobId,
			jurisdiction: filter.Jurisdiction, interval: proto.Clone(filter.LegalInterval).(*pb.LegalInterval),
			status: filter.LegalStatus, indexable: true,
		}
	}
	return batch, view
}

func TestValidateIndexBatchLocalClosure(t *testing.T) {
	base, view := localIndexBatchFixture()
	if err := ValidateIndexBatchLocalClosure(base, view, 7, 10); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*pb.IndexBatch){
		"wrong generation":       func(b *pb.IndexBatch) { b.Records[0].GenerationId = "generation:other" },
		"missing accepted count": func(b *pb.IndexBatch) { b.Counts.Accepted = 0; b.Counts.Rejected = 1 },
		"wrong legal source":     func(b *pb.IndexBatch) { b.Records[0].FilterMetadata.ProvisionFilters[0].SourceBlobId = "blob:forged" },
		"duplicate record": func(b *pb.IndexBatch) {
			b.Records = append(b.Records, proto.Clone(b.Records[0]).(*pb.IndexRecord))
			b.Counts.Expected++
			b.Counts.Accepted++
		},
		"close and upsert same ID": func(b *pb.IndexBatch) {
			b.Closures = []*pb.VisibilityClosure{{RecordId: b.Records[0].Meta.RecordId, ExpectedFromSeq: 1, ToSeq: 7}}
		},
		"foreign corpus":    func(b *pb.IndexBatch) { b.Context.CorpusId = "corpus:other" },
		"wrong dense model": func(b *pb.IndexBatch) { b.Records[0].DenseVector.ModelId = "model:other" },
		"missing sparse":    func(b *pb.IndexBatch) { b.Records[0].SparseVector = nil },
		"wrong model task":  func(b *pb.IndexBatch) { b.Generation.DenseManifest.Task = pb.ModelTask_MODEL_TASK_RERANK },
		"wrong target": func(b *pb.IndexBatch) {
			b.Records[0].Meta.Visibility.FromSeq = 8
			b.Records[0].FilterMetadata.Visibility.FromSeq = 8
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			batch := proto.Clone(base).(*pb.IndexBatch)
			mutate(batch)
			if err := ValidateIndexBatchLocalClosure(batch, view, 7, 10); err == nil {
				t.Fatal("invalid index batch accepted")
			}
		})
	}
	closureOnly := proto.Clone(base).(*pb.IndexBatch)
	closureOnly.Records = nil
	closureOnly.Counts = &pb.Counts{}
	closureOnly.Closures = []*pb.VisibilityClosure{{RecordId: "index:old", ExpectedFromSeq: 1, ToSeq: 7}}
	if err := ValidateIndexBatchLocalClosure(closureOnly, view, 7, 10); err != nil {
		t.Fatalf("valid closure-only batch rejected: %v", err)
	}
	closureOnly.Closures = append(closureOnly.Closures,
		&pb.VisibilityClosure{RecordId: "index:second", ExpectedFromSeq: 1, ToSeq: 8})
	if err := ValidateIndexBatchLocalClosure(closureOnly, view, 7, 10); err == nil {
		t.Fatal("closure to another target sequence accepted")
	}
}
