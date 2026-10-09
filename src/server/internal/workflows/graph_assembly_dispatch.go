// Executes a storage-authorized ASSEMBLE assignment through the batch worker and
// verifies returned GraphDelta bytes against its exact source projection. Reads
// are bounded before I/O, the RPC observes caller/pin/claim deadlines, and live
// authority is rechecked after validation. This library does not advance jobs or
// publish graph storage: durable fenced output commit remains a separate step.
// Measure read/hash/RPC/admission p95, cancellation lag and peak RSS against
// configs/benchmark-targets.yaml; fixture parity is not a production benchmark.
package workflows

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphDispatchAuthority interface {
	AuthorizeGraphDispatch(context.Context, domain.SnapshotPin, domain.JobRecord) (domain.GraphJobAssignment, error)
}

type GraphBatchWorker interface {
	ProcessBatch(context.Context, *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error)
}

// VerifiedGraphOutput owns its values so a caller cannot mutate an admitted
// delta before registration/commit. It is not a durable completion receipt.
type VerifiedGraphOutput struct {
	assignment domain.GraphJobAssignment
	response   *pb.ProcessBatchResponse
	delta      *pb.GraphDelta
}

func (o *VerifiedGraphOutput) Response() *pb.ProcessBatchResponse {
	if o == nil || o.response == nil {
		return nil
	}
	return proto.Clone(o.response).(*pb.ProcessBatchResponse)
}

func (o *VerifiedGraphOutput) Delta() *pb.GraphDelta {
	if o == nil || o.delta == nil {
		return nil
	}
	return proto.Clone(o.delta).(*pb.GraphDelta)
}

func ExecuteGraphAssembly(ctx context.Context, authority GraphDispatchAuthority, reader DocumentArtifactReader,
	worker GraphBatchWorker, pin domain.SnapshotPin, job domain.JobRecord, call *pb.RequestContext, ontology *domain.Ontology) (*VerifiedGraphOutput, error) {
	if ctx == nil || authority == nil || reader == nil || worker == nil || ontology == nil {
		return nil, errors.New("graph execution dependencies required")
	}
	if err := domain.ValidateWire(call, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	ownedCall := proto.Clone(call).(*pb.RequestContext)
	deadline := ownedCall.Deadline.AsTime()
	if pin.ExpiresAt.Before(deadline) {
		deadline = pin.ExpiresAt
	}
	if job.LeaseExpiresAt.Before(deadline) {
		deadline = job.LeaseExpiresAt
	}
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	ownedCall.Deadline = timestamppb.New(deadline)
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	a, err := authority.AuthorizeGraphDispatch(bounded, pin, job)
	if err != nil {
		return nil, err
	}
	request, err := domain.BuildGraphAssemblyRequest(a, job, ownedCall)
	if err != nil {
		return nil, err
	}
	remaining := uint64(domain.DefaultWireLimits.MaxBytes)
	read := func(ref *pb.ArtifactRef, m proto.Message) error {
		raw, err := readGraphBytes(bounded, reader, ref, &remaining)
		if err != nil {
			return err
		}
		return domain.DecodeWire(raw, m, domain.DefaultWireLimits)
	}
	plan := new(pb.GraphAssemblyPlan)
	if err = read(a.Reference, plan); err != nil {
		return nil, err
	}
	if !proto.Equal(plan, a.Plan) || !proto.Equal(plan.OntologyHash, ontology.ContentHash()) {
		return nil, errors.New("stored graph plan or ontology drift")
	}
	in := domain.GraphOutputSources{Document: new(pb.DocumentBatch), Extraction: new(pb.ExtractionBatch), Resolution: new(pb.ResolutionBatch), Registry: new(pb.RegistryEntityView), NormalizedTexts: map[string][]byte{}}
	for _, role := range []struct {
		ref    *pb.ArtifactRef
		target proto.Message
	}{
		{plan.DocumentBatch, in.Document}, {plan.ExtractionBatch, in.Extraction}, {plan.ResolutionBatch, in.Resolution}, {plan.RegistryView, in.Registry},
	} {
		if err = read(role.ref, role.target); err != nil {
			return nil, err
		}
	}
	if err = domain.ValidateExtractionBatchClosure(in.Extraction, in.Document, domain.DefaultWireLimits.MaxItems); err != nil {
		return nil, err
	}
	// Precharge required text descriptors before any text I/O, then read each once.
	textBudget := remaining
	if err = domain.BudgetGraphAssemblyTexts(in.Document, in.Extraction, &textBudget); err != nil {
		return nil, err
	}
	needed := map[string]bool{}
	for _, m := range in.Extraction.Mentions {
		needed[m.TextSpan.TextArtifactId] = true
	}
	for _, s := range in.Extraction.Supports {
		for _, span := range s.EvidenceSpans {
			needed[span.TextArtifactId] = true
		}
	}
	for _, text := range in.Document.TextArtifacts {
		if !needed[text.Meta.RecordId] {
			continue
		}
		raw, err := readGraphBytes(bounded, reader, text.NormalizedTextRef, &remaining)
		if err != nil {
			return nil, err
		}
		in.NormalizedTexts[text.Meta.RecordId] = raw
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	response, err := worker.ProcessBatch(bounded, proto.Clone(request).(*pb.ProcessBatchRequest))
	if err != nil {
		return nil, err
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	if err = domain.ValidateGraphWorkerEnvelope(request, response, plan); err != nil {
		return nil, err
	}
	// Output is independently capped: the worker may output more bytes than the
	// remaining input allowance, but never more than one bounded wire artifact.
	outputRemaining := uint64(domain.DefaultWireLimits.MaxBytes)
	raw, err := readGraphBytes(bounded, reader, response.GraphDelta, &outputRemaining)
	if err != nil {
		return nil, err
	}
	delta := new(pb.GraphDelta)
	if err = domain.DecodeWire(raw, delta, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if err = domain.ValidatePlannedGraphDelta(delta, plan, in, ontology); err != nil {
		return nil, err
	}
	again, err := authority.AuthorizeGraphDispatch(bounded, pin, job)
	if err != nil {
		return nil, err
	}
	if again.JobID != a.JobID || again.SourceJobID != a.SourceJobID || !proto.Equal(again.Plan, a.Plan) || !proto.Equal(again.Reference, a.Reference) {
		return nil, errors.New("graph assignment changed during execution")
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	ownedAssignment := domain.GraphJobAssignment{JobID: a.JobID, SourceJobID: a.SourceJobID,
		Plan: proto.Clone(plan).(*pb.GraphAssemblyPlan), Reference: proto.Clone(a.Reference).(*pb.ArtifactRef)}
	return &VerifiedGraphOutput{assignment: ownedAssignment, response: proto.Clone(response).(*pb.ProcessBatchResponse), delta: delta}, nil
}

func readGraphBytes(ctx context.Context, reader DocumentArtifactReader, ref *pb.ArtifactRef, remaining *uint64) ([]byte, error) {
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if ref.SchemaVersion != 1 || ref.ByteSize == 0 || ref.ByteSize > *remaining {
		return nil, errors.New("graph artifact byte budget exceeded")
	}
	*remaining -= ref.ByteSize
	raw, err := reader.ReadVerified(ctx, ref, ref.ByteSize)
	if err != nil {
		return nil, err
	}
	if uint64(len(raw)) != ref.ByteSize || fmt.Sprintf("%x", sha256.Sum256(raw)) != ref.ContentHash.Sha256 {
		return nil, errors.New("graph artifact hash/size mismatch")
	}
	return raw, nil
}
