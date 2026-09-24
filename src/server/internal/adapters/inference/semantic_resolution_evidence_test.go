// Tests candidate-side provenance and two-sided citation enforcement at the real gateway.
// Synthetic provider results isolate validation from LLM quality; unmeasured release accuracy
// and latency targets still require frozen models, gold pairs and production workload runs.
package inference

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestResolveRejectsInvalidCandidateContextBeforeProvider(t *testing.T) {
	cases := map[string]func(*pb.AmbiguousMention){
		"unknown canonical": func(i *pb.AmbiguousMention) { i.CandidateContexts[0].CanonicalId = "canonical:unknown" },
		"foreign corpus":    func(i *pb.AmbiguousMention) { i.CandidateContexts[0].SupportMention.Meta.CorpusId = "corpus:foreign" },
		"wrong type":        func(i *pb.AmbiguousMention) { i.CandidateContexts[0].SupportMention.CandidateType = "regulation" },
		"wrong source": func(i *pb.AmbiguousMention) {
			i.CandidateContexts[0].ContextItems[0].Provenance.Sources[0].SourceBlobId = "source:foreign"
		},
		"wrong quote": func(i *pb.AmbiguousMention) { i.CandidateContexts[0].SupportMention.SurfaceForm = "salah" },
		"duplicate support": func(i *pb.AmbiguousMention) {
			i.CandidateContexts = append(i.CandidateContexts, proto.Clone(i.CandidateContexts[0]).(*pb.ResolutionCandidateContext))
		},
		"context ID collision": func(i *pb.AmbiguousMention) { i.CandidateContexts[0].ContextItems[0].ItemId = i.ContextItems[0].ItemId },
		"absent context":       func(i *pb.AmbiguousMention) { i.CandidateContexts[0].ContextItems = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			provider := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
			service, request := resolutionFixture(provider)
			mutate(request.Items[0])
			if _, err := service.ResolveBatch(context.Background(), request); err == nil || provider.callCount() != 0 {
				t.Fatalf("invalid evidence reached provider: %v", err)
			}
		})
	}
}

func TestResolveLinkMustCiteBothSidesButDeferCanExplainMissingEvidence(t *testing.T) {
	for _, name := range []string{"missing candidate context", "only mention citation", "only candidate citation"} {
		t.Run(name, func(t *testing.T) {
			provider := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
			service, request := resolutionFixture(provider)
			switch name {
			case "missing candidate context":
				request.Items[0].CandidateContexts = nil
			case "only mention citation":
				provider.raw = json.RawMessage(strings.Replace(validResolutionJSON, `,"chunk:candidate"`, "", 1))
			case "only candidate citation":
				provider.raw = json.RawMessage(strings.Replace(validResolutionJSON, `"chunk:1",`, "", 1))
			}
			response, err := service.ResolveBatch(context.Background(), request)
			if err != nil || response.Results[0].GetError() == nil {
				t.Fatalf("unsupported LINK accepted: %v %v", response, err)
			}
		})
	}
	provider := &providerDouble{raw: json.RawMessage(`{"action":"DEFER","candidate_id":"","rationale":"Candidate source is unavailable.","evidence_item_ids":["chunk:1"]}`)}
	service, request := resolutionFixture(provider)
	request.Items[0].CandidateContexts = nil
	response, err := service.ResolveBatch(context.Background(), request)
	if err != nil || response.Results[0].GetProposal().GetAction() != pb.ResolutionAction_RESOLUTION_ACTION_DEFER {
		t.Fatalf("missing-evidence DEFER failed: %v %v", response, err)
	}
}

func TestResolveCannotCiteDifferentCandidatesEvidenceForLink(t *testing.T) {
	provider := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
	service, request := resolutionFixture(provider)
	second := proto.Clone(request.Items[0].Candidates[0]).(*pb.CanonicalEntity)
	second.Meta.RecordId = "canonical:second"
	request.Items[0].Candidates = append(request.Items[0].Candidates, second)
	request.Items[0].CandidateContexts[0].CanonicalId = second.Meta.RecordId
	response, err := service.ResolveBatch(context.Background(), request)
	if err != nil || response.Results[0].GetError() == nil {
		t.Fatalf("other candidate evidence authorized LINK: %v %v", response, err)
	}
}
