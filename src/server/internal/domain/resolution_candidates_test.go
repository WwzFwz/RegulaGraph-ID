// Exercises the pre-decision candidate boundary with forged LINK, hidden DEFER, stale revisions,
// and source evidence drift. Fixtures prove structural rejection, not legal entity accuracy.
package domain

import (
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func proposalCandidateFixture() (*pb.ExtractionBatch, *pb.ArtifactRef, *pb.RegistryCandidateBatch, *pb.ResolutionProposal) {
	candidates, source, sourceRef := candidateBatchFixture()
	candidates.Aliases = []*pb.Alias{{
		Meta:        &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "alias:one"},
		CanonicalId: "canonical:one", Surface: "Instansi A", NormalizedLookup: "instansi a",
		Scope: "national", Language: "id", SupportRefs: []string{"mention:source"},
	}}
	proposal := &pb.ResolutionProposal{
		Meta:       &pb.RecordMeta{SchemaVersion: 1, CorpusId: source.Meta.CorpusId, RecordId: "proposal:one"},
		MentionIds: []string{"mention:one"}, CandidateIds: []string{"canonical:one"},
		Action: pb.ResolutionAction_RESOLUTION_ACTION_LINK,
		Evidence: &pb.Provenance{
			Sources: []*pb.SourceVersionRef{proto.Clone(source.Mentions[0].SourceRefs[0]).(*pb.SourceVersionRef)},
			Spans:   []*pb.TextSpan{proto.Clone(source.Mentions[0].TextSpan).(*pb.TextSpan)},
		},
		Method: "reviewed_candidate", ExpectedRegistryRevision: candidates.RegistryRevision,
		LocalCorrelationId: "correlation:one",
	}
	return source, sourceRef, candidates, proposal
}

func TestValidateResolutionProposalsAgainstCandidatesBindsLinkAndDefer(t *testing.T) {
	source, sourceRef, candidates, proposal := proposalCandidateFixture()
	if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		[]*pb.ResolutionProposal{proposal}, 16, 3); err != nil {
		t.Fatal("valid LINK proposal rejected:", err)
	}
	proposal.Action = pb.ResolutionAction_RESOLUTION_ACTION_DEFER
	if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		[]*pb.ResolutionProposal{proposal}, 16, 3); err != nil {
		t.Fatal("valid DEFER proposal rejected:", err)
	}
	candidates.Lookups[0].Scopes[0].CandidateIds = nil
	candidates.Lookups[0].Scopes[0].Revision.EmptyResult = true
	candidates.Dependencies.LookupScopeRevisions[0].EmptyResult = true
	candidates.Candidates = nil
	candidates.Aliases = nil
	proposal.CandidateIds = nil
	if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		[]*pb.ResolutionProposal{proposal}, 16, 3); err != nil {
		t.Fatal("negative lookup DEFER rejected:", err)
	}
}

func TestValidateResolutionProposalsAgainstCandidatesRejectsForgedDecisionInputs(t *testing.T) {
	source, sourceRef, candidates, base := proposalCandidateFixture()
	cases := map[string]func(*pb.ResolutionProposal){
		"foreign canonical":       func(proposal *pb.ResolutionProposal) { proposal.CandidateIds[0] = "canonical:foreign" },
		"stale registry revision": func(proposal *pb.ResolutionProposal) { proposal.ExpectedRegistryRevision-- },
		"invented mention":        func(proposal *pb.ResolutionProposal) { proposal.MentionIds[0] = "mention:foreign" },
		"invented source":         func(proposal *pb.ResolutionProposal) { proposal.Evidence.Sources[0].SourceBlobId = "source:foreign" },
		"invented span":           func(proposal *pb.ResolutionProposal) { proposal.Evidence.Spans[0].StartByte++ },
		"automatic CREATE":        func(proposal *pb.ResolutionProposal) { proposal.Action = pb.ResolutionAction_RESOLUTION_ACTION_CREATE },
		"missing method":          func(proposal *pb.ResolutionProposal) { proposal.Method = "" },
		"smuggled identity key": func(proposal *pb.ResolutionProposal) {
			proposal.ProposedIdentityKeys = []*pb.IdentityKey{{Namespace: "test", Value: "unexpected"}}
		},
		"record ID collides with mention": func(proposal *pb.ResolutionProposal) {
			proposal.Meta.RecordId = "mention:one"
		},
		"premature visibility": func(proposal *pb.ResolutionProposal) {
			proposal.Meta.Visibility = &pb.Visibility{FromSeq: 1}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			proposal := proto.Clone(base).(*pb.ResolutionProposal)
			mutate(proposal)
			if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
				[]*pb.ResolutionProposal{proposal}, 16, 3); err == nil {
				t.Fatal("forged proposal was accepted")
			}
		})
	}
	deferProposal := proto.Clone(base).(*pb.ResolutionProposal)
	deferProposal.Action = pb.ResolutionAction_RESOLUTION_ACTION_DEFER
	deferProposal.CandidateIds = nil
	if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		[]*pb.ResolutionProposal{deferProposal}, 16, 3); err == nil {
		t.Fatal("DEFER hid a valid candidate")
	}
	if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		[]*pb.ResolutionProposal{base, proto.Clone(base).(*pb.ResolutionProposal)}, 16, 3); err == nil {
		t.Fatal("duplicate mention proposal was accepted")
	}
	candidates.Aliases = nil
	if err := ValidateResolutionProposalsAgainstCandidates(source, sourceRef, candidates,
		[]*pb.ResolutionProposal{base}, 16, 3); err == nil {
		t.Fatal("LINK to a candidate without matching sourced alias was accepted")
	}
}
