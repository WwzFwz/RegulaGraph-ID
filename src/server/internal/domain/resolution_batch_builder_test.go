// Verifies that the RESOLVE artifact retains receipt decisions and both positive/negative
// dependencies, while malformed receipts never become publishable batches. This fixture is
// structural and does not measure model accuracy or production latency.
package domain

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func resolutionBuilderFixture() (*pb.ExtractionBatch, *pb.ArtifactRef, *pb.RegistryCandidateBatch,
	*pb.ArtifactRef, *pb.RegistryResolveRequest, *pb.RegistryResolveResponse,
	*pb.ModelManifest, *pb.ProducerManifest, *pb.TokenUsage) {
	source, sourceRef, candidates, request, response := registryReceiptFixture()
	source.Context = proto.Clone(request.Context).(*pb.RequestContext)
	source.Context.RequestId = "request:extract"
	source.Context.TraceId = "trace:extract"
	candidates.Context = proto.Clone(request.Context).(*pb.RequestContext)
	sourceRef.StorageKey = "objects/extract"
	sourceRef.MediaType = "application/x-protobuf"
	sourceRef.SchemaVersion = 1
	candidates.SourceExtractionBatch = proto.Clone(sourceRef).(*pb.ArtifactRef)
	candidateRef := &pb.ArtifactRef{
		ArtifactId: "artifact:candidates", ContentHash: &pb.ContentHash{Sha256: strings.Repeat("d", 64)},
		StorageKey: "objects/candidates", MediaType: "application/x-protobuf", SchemaVersion: 1,
	}
	model := &pb.ModelManifest{
		ModelId: "resolver", Version: "v1", Task: pb.ModelTask_MODEL_TASK_RESOLVE,
		WeightsHash:   &pb.ContentHash{Sha256: strings.Repeat("e", 64)},
		TokenizerHash: &pb.ContentHash{Sha256: strings.Repeat("f", 64)},
		MaxTokens:     128, Precision: "fp32", Backend: "test",
	}
	producer := &pb.ProducerManifest{
		Software: "resolve-worker", Build: "test-build", SchemaVersion: 1,
		Models:     []*pb.ModelManifest{proto.Clone(model).(*pb.ModelManifest)},
		ConfigHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)},
	}
	usage := &pb.TokenUsage{TokenizerId: "resolver-tokenizer"}
	return source, sourceRef, candidates, candidateRef, request, response, model, producer, usage
}

func TestAssembleResolutionBatchFromReceiptBindsInputsAndCopies(t *testing.T) {
	source, sourceRef, candidates, candidateRef, request, response, model, producer, usage := resolutionBuilderFixture()
	batch, err := AssembleResolutionBatchFromReceipt(source, sourceRef, candidates, candidateRef,
		request, response, model, producer, usage, "resolution:built", 32, 3)
	if err != nil {
		t.Fatal("valid receipt did not assemble:", err)
	}
	if len(batch.Dependencies.Dependencies) != 2 ||
		batch.Dependencies.Dependencies[0].DependencyId != sourceRef.ArtifactId ||
		batch.Dependencies.Dependencies[1].DependencyId != candidateRef.ArtifactId ||
		len(batch.Dependencies.LookupScopeRevisions) != 1 ||
		!proto.Equal(batch.Dependencies.LookupScopeRevisions[0], candidates.Dependencies.LookupScopeRevisions[0]) ||
		batch.Decisions[0].AssignedCanonicalIds[0] != "canonical:one" ||
		batch.Context.RequestId != request.Context.RequestId ||
		batch.Context.RequestId == source.Context.RequestId {
		t.Fatal("resolution output lost source, candidate, lookup, or registry decision")
	}
	request.Proposals[0].CandidateIds[0] = "canonical:mutated"
	response.Assignments[0].GetDecision().AssignedCanonicalIds[0] = "canonical:mutated"
	if batch.Proposals[0].CandidateIds[0] != "canonical:one" ||
		batch.Decisions[0].AssignedCanonicalIds[0] != "canonical:one" {
		t.Fatal("resolution artifact retained mutable caller records")
	}
}

func TestAssembleResolutionBatchFromReceiptRejectsInvalidReceipt(t *testing.T) {
	source, sourceRef, candidates, candidateRef, request, response, model, producer, usage := resolutionBuilderFixture()
	response.Assignments = nil
	if _, err := AssembleResolutionBatchFromReceipt(source, sourceRef, candidates, candidateRef,
		request, response, model, producer, usage, "resolution:built", 32, 3); err == nil {
		t.Fatal("partial registry receipt became a complete resolution artifact")
	}
}

func TestAssembleResolutionBatchFromReceiptRejectsInputIdentityCollision(t *testing.T) {
	source, sourceRef, candidates, candidateRef, request, response, model, producer, usage := resolutionBuilderFixture()
	for _, id := range []string{source.Meta.RecordId, candidates.Meta.RecordId,
		candidates.Candidates[0].Meta.RecordId, candidates.Aliases[0].Meta.RecordId,
		request.Proposals[0].Meta.RecordId, response.Assignments[0].GetDecision().Meta.RecordId} {
		if _, err := AssembleResolutionBatchFromReceipt(source, sourceRef, candidates, candidateRef,
			request, response, model, producer, usage, id, 32, 3); err == nil {
			t.Fatalf("batch ID %q collided with an input record", id)
		}
	}
	candidateRef.ArtifactId = sourceRef.ArtifactId
	if _, err := AssembleResolutionBatchFromReceipt(source, sourceRef, candidates, candidateRef,
		request, response, model, producer, usage, "resolution:built", 32, 3); err == nil {
		t.Fatal("candidate artifact reused extraction artifact identity")
	}
}

func TestAssembleResolutionBatchFromReceiptRejectsOversizedProducerBeforeCopy(t *testing.T) {
	source, sourceRef, candidates, candidateRef, request, response, model, producer, usage := resolutionBuilderFixture()
	producer.Build = strings.Repeat("x", DefaultWireLimits.MaxBytes)
	if batch, err := AssembleResolutionBatchFromReceipt(source, sourceRef, candidates, candidateRef,
		request, response, model, producer, usage, "resolution:built", 32, 3); err == nil || batch != nil {
		t.Fatal("oversized producer was copied into a resolution artifact")
	}
}
