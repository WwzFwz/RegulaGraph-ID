// The publication filter tests prevent accidental zipping of legal interval,
// status and source arrays across different provision versions. They do not
// authenticate the source DocumentBatch or exercise Qdrant storage.
package domain

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func indexFilterFixture() *pb.IndexRecord {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	date := &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN}
	interval := &pb.LegalInterval{Start: date, End: date}
	visibility := &pb.Visibility{FromSeq: 7}
	return &pb.IndexRecord{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "index:one",
			Visibility: proto.Clone(visibility).(*pb.Visibility)},
		GenerationId:         "generation:one",
		ChunkId:              "chunk:one",
		ProvisionVersionRefs: []string{"version:one", "version:two"},
		FilterMetadata: &pb.FilterMetadata{Visibility: visibility, ProvisionFilters: []*pb.IndexProvisionFilter{
			{ProvisionVersionId: "version:one", RegulationId: "regulation:one", SourceBlobId: "blob:one",
				LegalInterval: proto.Clone(interval).(*pb.LegalInterval), LegalStatus: pb.LegalStatus_LEGAL_STATUS_UNKNOWN, Jurisdiction: "ID"},
			{ProvisionVersionId: "version:two", RegulationId: "regulation:two", SourceBlobId: "blob:two",
				LegalInterval: proto.Clone(interval).(*pb.LegalInterval), LegalStatus: pb.LegalStatus_LEGAL_STATUS_UNKNOWN, Jurisdiction: "ID"},
		}},
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:one", ProducerManifest: &pb.ProducerManifest{
			Software: "fixture", Build: "fixture", SchemaVersion: 1, ConfigHash: hash,
		}},
	}
}

func TestValidatePairedIndexGenerationRequiresKnownFilterFormat(t *testing.T) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	artifact := &pb.ArtifactRef{ArtifactId: "artifact:lexical", ContentHash: hash,
		StorageKey: "objects/lexical", MediaType: "application/x-protobuf", SchemaVersion: 1}
	generation := &pb.IndexGeneration{
		Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "generation:one"},
		DenseManifest: &pb.ModelManifest{ModelId: "model:one", Version: "1", WeightsHash: hash,
			TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EMBED, Dimensions: proto.Uint32(768), MaxTokens: 512,
			Precision: "fp32", Backend: "fixture"},
		LexicalAnalyzer:      proto.Clone(artifact).(*pb.ArtifactRef),
		LexicalDictionary:    proto.Clone(artifact).(*pb.ArtifactRef),
		LexicalStatistics:    proto.Clone(artifact).(*pb.ArtifactRef),
		OntologyVersion:      "ontology:v1",
		FilterFormat:         pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1,
		EmbeddingInputPolicy: "structure-labels-v1",
	}
	if err := ValidatePairedIndexGeneration(generation); err != nil {
		t.Fatal(err)
	}
	generation.FilterFormat = pb.IndexFilterFormat_INDEX_FILTER_FORMAT_UNSPECIFIED
	if err := ValidatePairedIndexGeneration(generation); err == nil {
		t.Fatal("unmarked legacy generation accepted as paired")
	}
	generation.FilterFormat = 99
	if err := ValidatePairedIndexGeneration(generation); err == nil {
		t.Fatal("unknown future filter format accepted")
	}
	generation.FilterFormat = pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1
	generation.EmbeddingInputPolicy = "parent-labels-v1"
	if err := ValidatePairedIndexGeneration(generation); err == nil {
		t.Fatal("unsupported embedding input policy accepted")
	}
}

func TestValidatePairedIndexFiltersRequiresVersionClosure(t *testing.T) {
	valid := indexFilterFixture()
	if err := ValidatePairedIndexFilters(valid); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*pb.IndexRecord){
		"version omitted": func(r *pb.IndexRecord) { r.FilterMetadata.ProvisionFilters = r.FilterMetadata.ProvisionFilters[:1] },
		"foreign version": func(r *pb.IndexRecord) { r.FilterMetadata.ProvisionFilters[1].ProvisionVersionId = "version:other" },
		"duplicate version source": func(r *pb.IndexRecord) {
			r.FilterMetadata.ProvisionFilters[1].ProvisionVersionId = "version:one"
			r.FilterMetadata.ProvisionFilters[1].SourceBlobId = "blob:one"
		},
		"legacy array mix": func(r *pb.IndexRecord) {
			r.FilterMetadata.LegalStatuses = []pb.LegalStatus{pb.LegalStatus_LEGAL_STATUS_ACTIVE}
		},
		"visibility drift":    func(r *pb.IndexRecord) { r.FilterMetadata.Visibility.FromSeq++ },
		"nil interval":        func(r *pb.IndexRecord) { r.FilterMetadata.ProvisionFilters[0].LegalInterval = nil },
		"unknown status enum": func(r *pb.IndexRecord) { r.FilterMetadata.ProvisionFilters[0].LegalStatus = 0 },
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			record := proto.Clone(valid).(*pb.IndexRecord)
			mutation(record)
			if err := ValidatePairedIndexFilters(record); err == nil {
				t.Fatal("invalid paired filter accepted")
			}
		})
	}
}

func TestIndexSourceViewBindsLegalFiltersToVerifiedChunk(t *testing.T) {
	input, binding := bindingFixture()
	candidates, err := PlanProvisionIdentities(input, []RegulationDocumentBinding{binding}, 100)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindDocumentBatch(input, []RegulationDocumentBinding{binding}, candidates,
		provisionAssignments(t, candidates, 8), bindingConfig(8))
	if err != nil {
		t.Fatal(err)
	}
	version := bound.Versions[0]
	bound.Chunks = []*pb.Chunk{{
		Meta:                 &pb.RecordMeta{SchemaVersion: 1, CorpusId: bound.Meta.CorpusId, RecordId: "chunk:index:1"},
		ProvisionVersionRefs: []string{version.Meta.RecordId},
		TextSpan:             proto.Clone(version.Spans[0]).(*pb.TextSpan),
		StructureNodeRefs:    []string{"structure:root"},
		ChunkerManifest:      fixtureProducerManifest(),
		TokenCounts:          []*pb.TokenUsage{{InputTokens: 10, TokenizerId: "tokenizer:test"}},
	}}
	// An unrelated rejected version may remain in the source batch, but cannot
	// be admitted as a record in this index generation.
	bound.Versions[1].ReviewState = pb.ReviewState_REVIEW_STATE_REJECTED
	view, err := NewIndexSourceView(bound, 1000)
	if err != nil {
		t.Fatal(err)
	}
	provisionID := version.ProvisionId
	var regulationID string
	for _, provision := range bound.Provisions {
		if provision.Meta.RecordId == provisionID {
			regulationID = provision.RegulationId
		}
	}
	record := indexFilterFixture()
	record.Meta.CorpusId = bound.Meta.CorpusId
	record.ChunkId = "chunk:index:1"
	record.ProvisionVersionRefs = []string{version.Meta.RecordId}
	record.FilterMetadata.ProvisionFilters = []*pb.IndexProvisionFilter{{
		ProvisionVersionId: version.Meta.RecordId,
		RegulationId:       regulationID,
		SourceBlobId:       "source-blob:a",
		LegalInterval:      proto.Clone(version.LegalInterval).(*pb.LegalInterval),
		LegalStatus:        version.LegalStatus,
		Jurisdiction:       "ID",
	}}
	if err := view.ValidateRecord(record); err != nil {
		t.Fatal(err)
	}
	// The view owns its legal facts after construction.
	bound.Versions[0].LegalStatus = pb.LegalStatus_LEGAL_STATUS_ACTIVE
	if err := view.ValidateRecord(record); err != nil {
		t.Fatalf("source mutation changed verified view: %v", err)
	}
	mutations := map[string]func(*pb.IndexRecord){
		"wrong source":     func(r *pb.IndexRecord) { r.FilterMetadata.ProvisionFilters[0].SourceBlobId = "blob:other" },
		"wrong regulation": func(r *pb.IndexRecord) { r.FilterMetadata.ProvisionFilters[0].RegulationId = "regulation:other" },
		"invented status": func(r *pb.IndexRecord) {
			r.FilterMetadata.ProvisionFilters[0].LegalStatus = pb.LegalStatus_LEGAL_STATUS_ACTIVE
		},
		"invented interval": func(r *pb.IndexRecord) {
			r.FilterMetadata.ProvisionFilters[0].LegalInterval.Start.Knowledge = pb.DateKnowledge_DATE_KNOWLEDGE_UNBOUNDED
		},
		"wrong chunk":  func(r *pb.IndexRecord) { r.ChunkId = "chunk:other" },
		"wrong corpus": func(r *pb.IndexRecord) { r.Meta.CorpusId = "corpus:other" },
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := proto.Clone(record).(*pb.IndexRecord)
			mutation(candidate)
			if err := view.ValidateRecord(candidate); err == nil {
				t.Fatal("index record detached from source was accepted")
			}
		})
	}
	bound.Versions[0].LegalStatus = record.FilterMetadata.ProvisionFilters[0].LegalStatus
	expired := proto.Clone(bound).(*pb.DocumentBatch)
	baselineView, err := NewIndexSourceView(expired, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := baselineView.ValidateRecord(record); err != nil {
		t.Fatalf("expiry baseline must be valid before mutation: %v", err)
	}
	expired.Versions[0].Meta.Visibility = &pb.Visibility{FromSeq: 1, ToSeq: proto.Uint64(2)}
	expiredView, err := NewIndexSourceView(expired, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := expiredView.ValidateRecord(record); err == nil {
		t.Fatal("index visibility outlived source version visibility")
	}
	sourceExpired := proto.Clone(bound).(*pb.DocumentBatch)
	sourceExpired.Sources[0].Meta.Visibility = &pb.Visibility{FromSeq: 1, ToSeq: proto.Uint64(2)}
	sourceView, err := NewIndexSourceView(sourceExpired, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceView.ValidateRecord(record); err == nil {
		t.Fatal("index visibility outlived source blob visibility")
	}
	bound.Versions[0].ReviewState = pb.ReviewState_REVIEW_STATE_REJECTED
	rejectedView, err := NewIndexSourceView(bound, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectedView.ValidateRecord(record); err == nil {
		t.Fatal("rejected source version was admitted to the index")
	}
}
