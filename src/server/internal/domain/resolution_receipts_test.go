// Exercises the registry receipt boundary with valid LINK/DEFER and adversarial response drift.
// These fixtures establish deterministic provenance/revision checks, not legal resolution
// accuracy or production latency; both remain REQUIRED_UNMEASURED.
package domain

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func registryReceiptFixture() (*pb.ExtractionBatch, *pb.ArtifactRef, *pb.RegistryCandidateBatch,
	*pb.RegistryResolveRequest, *pb.RegistryResolveResponse) {
	source, sourceRef, candidates, proposal := proposalCandidateFixture()
	context := proto.Clone(source.Context).(*pb.RequestContext)
	context.RequestId = "request:resolve"
	context.TraceId = "trace:resolve"
	context.Deadline = timestamppb.New(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	request := &pb.RegistryResolveRequest{
		Context: context, OperationKey: "resolve:one", ExpectedRevision: candidates.RegistryRevision,
		Proposals: []*pb.ResolutionProposal{proposal},
	}
	response := &pb.RegistryResolveResponse{
		RequestId: context.RequestId, RegistryRevision: candidates.RegistryRevision,
		Assignments: []*pb.RegistryAssignment{{
			ProposalId: proposal.Meta.RecordId, LocalCorrelationId: proposal.LocalCorrelationId,
			Result: &pb.RegistryAssignment_Decision{Decision: &pb.ResolutionDecision{
				Meta:       &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "decision:one"},
				ProposalId: proposal.Meta.RecordId, AssignedCanonicalIds: []string{"canonical:one"},
				Action: pb.ResolutionAction_RESOLUTION_ACTION_LINK, RegistryRevision: candidates.RegistryRevision,
				Actor: "registry", Reason: "reviewed evidence",
			}},
		}},
	}
	return source, sourceRef, candidates, request, response
}

func TestValidateRegistryResolveReceiptAcceptsBoundDecisions(t *testing.T) {
	source, ref, candidates, request, response := registryReceiptFixture()
	if err := ValidateRegistryResolveReceipt(source, ref, candidates, request, response, 16, 3); err != nil {
		t.Fatal("valid LINK receipt rejected:", err)
	}
	response.RegistryRevision++
	response.Assignments[0].GetDecision().RegistryRevision++
	if err := ValidateRegistryResolveReceipt(source, ref, candidates, request, response, 16, 3); err != nil {
		t.Fatal("valid one-step registry commit receipt rejected:", err)
	}
	request.Proposals[0].Action = pb.ResolutionAction_RESOLUTION_ACTION_DEFER
	response.Assignments[0].GetDecision().Action = pb.ResolutionAction_RESOLUTION_ACTION_DEFER
	response.Assignments[0].GetDecision().AssignedCanonicalIds = nil
	if err := ValidateRegistryResolveReceipt(source, ref, candidates, request, response, 16, 3); err != nil {
		t.Fatal("valid DEFER receipt rejected:", err)
	}
}

func TestValidateRegistryResolveReceiptRejectsForgedAndPartialResults(t *testing.T) {
	source, ref, candidates, baseRequest, baseResponse := registryReceiptFixture()
	cases := map[string]func(*pb.RegistryResolveRequest, *pb.RegistryResolveResponse){
		"wrong request": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.RequestId = "request:foreign"
		},
		"stale request": func(request *pb.RegistryResolveRequest, _ *pb.RegistryResolveResponse) {
			request.ExpectedRevision--
		},
		"unexplained receipt revision jump": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.RegistryRevision += 2
		},
		"foreign context": func(request *pb.RegistryResolveRequest, _ *pb.RegistryResolveResponse) {
			request.Context.AuthScopeRef = "scope:foreign"
		},
		"missing assignment": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments = nil
		},
		"foreign correlation": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].LocalCorrelationId = "correlation:foreign"
		},
		"foreign proposal": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].ProposalId = "proposal:foreign"
		},
		"foreign decision target": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].GetDecision().AssignedCanonicalIds[0] = "canonical:foreign"
		},
		"decision action drift": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].GetDecision().Action = pb.ResolutionAction_RESOLUTION_ACTION_CREATE
		},
		"decision revision drift": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].GetDecision().RegistryRevision--
		},
		"receipt advanced without decision": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.RegistryRevision++
		},
		"decision corpus drift": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].GetDecision().Meta.CorpusId = "corpus:foreign"
		},
		"decision reuses proposal ID": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].GetDecision().Meta.RecordId = "proposal:one"
		},
		"decision reuses mention ID": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].GetDecision().Meta.RecordId = "mention:one"
		},
		"decision reuses candidate ID": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].GetDecision().Meta.RecordId = "canonical:one"
		},
		"item error": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0].Result = &pb.RegistryAssignment_Error{Error: &pb.OperationError{
				Code: pb.ErrorCode_ERROR_CODE_CONFLICT, SafeMessage: "revision conflict",
			}}
		},
		"nil assignment": func(_ *pb.RegistryResolveRequest, response *pb.RegistryResolveResponse) {
			response.Assignments[0] = nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := proto.Clone(baseRequest).(*pb.RegistryResolveRequest)
			response := proto.Clone(baseResponse).(*pb.RegistryResolveResponse)
			mutate(request, response)
			if err := ValidateRegistryResolveReceipt(source, ref, candidates, request, response, 16, 3); err == nil {
				t.Fatal("forged or partial receipt accepted")
			}
		})
	}
}
