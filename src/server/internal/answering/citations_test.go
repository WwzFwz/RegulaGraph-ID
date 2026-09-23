// Exercises trusted citation construction from rendered evidence and versioned source metadata.
// These fixtures prove structural provenance, including multi-source locator requirements and
// URL lookup caching; they do not prove semantic support or answer quality benchmarks.
package answering

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestBuildCitationsUsesRenderedEvidenceAndTrustedURL(t *testing.T) {
	answer, rendered, bundle := groundedFixture(t)
	lookupCalls := 0
	lookup := func(blob, version string) ([]string, error) {
		lookupCalls++
		return trustedURL(blob, version)
	}
	citations, err := BuildCitations(answer.Claims, rendered, bundle, lookup, 4)
	if err != nil || len(citations) != 1 || citations[0].SourceUrl != "https://example.org/source.pdf" ||
		citations[0].SourceSpan == nil || citations[0].SourceSpan.TextArtifactId != "text:one" || lookupCalls != 1 {
		t.Fatalf("trusted citation not built: citations=%v calls=%d err=%v", citations, lookupCalls, err)
	}
	second, err := BuildCitations(answer.Claims, rendered, bundle, trustedURL, 4)
	if err != nil || !proto.Equal(citations[0], second[0]) {
		t.Fatalf("citation ID or metadata is not deterministic: %v %v", second, err)
	}
	answer.Citations = citations
	if err := ValidateGroundedAnswer(answer, rendered, bundle, trustedURL); err != nil {
		t.Fatalf("generated citation failed final structural gate: %v", err)
	}
	answer.Claims = append(answer.Claims, &pb.Claim{ClaimId: "claim:two",
		AnswerTextSpan: &pb.AnswerTextSpan{StartByte: 1, EndByte: 2},
		EvidenceIds:    []string{"evidence:two"}, SupportStatus: pb.SupportStatus_SUPPORT_STATUS_SUPPORTED})
	citations, err = BuildCitations(answer.Claims, rendered, bundle, lookup, 4)
	if err != nil || len(citations) != 2 || lookupCalls != 2 {
		t.Fatalf("batch URL lookup was not cached: citations=%v calls=%d err=%v", citations, lookupCalls, err)
	}
	if _, err = BuildCitations(answer.Claims, rendered, bundle, trustedURL, 1); err == nil {
		t.Fatal("citation cap ignored")
	}
	unsupported := proto.Clone(answer.Claims[0]).(*pb.Claim)
	unsupported.ClaimId = "claim:unsupported"
	unsupported.SupportStatus = pb.SupportStatus_SUPPORT_STATUS_UNSUPPORTED
	if citations, err = BuildCitations([]*pb.Claim{unsupported}, rendered, bundle, trustedURL, 1); err != nil || len(citations) != 0 {
		t.Fatalf("unsupported claim consumed citation output budget: %v %v", citations, err)
	}
	many := make([]*pb.Claim, maximumCitationClaims+1)
	for index := range many {
		many[index] = unsupported
	}
	if _, err = BuildCitations(many, rendered, bundle, trustedURL, 1); err == nil {
		t.Fatal("unsupported claim flood ignored input limit")
	}
}

func TestBuildCitationsRejectsUnrenderedAndUntrustedSources(t *testing.T) {
	answer, rendered, bundle := groundedFixture(t)
	mutated := proto.Clone(answer.Claims[0]).(*pb.Claim)
	mutated.EvidenceIds = []string{"evidence:absent"}
	if _, err := BuildCitations([]*pb.Claim{mutated}, rendered, bundle, trustedURL, 4); err == nil {
		t.Fatal("fabricated evidence ID was cited")
	}
	partial := proto.Clone(rendered).(*pb.ContextBundle)
	partial.OrderedEvidenceIds = nil
	partial.RenderedBlocks = nil
	partial.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	if _, err := BuildCitations(answer.Claims, partial, bundle, trustedURL, 4); err == nil {
		t.Fatal("evidence omitted from context was cited")
	}
	forged := proto.Clone(rendered).(*pb.ContextBundle)
	forged.RenderedBlocks[0].RenderedText = "invented text"
	if _, err := BuildCitations(answer.Claims, forged, bundle, trustedURL, 4); err == nil {
		t.Fatal("forged rendered text was cited")
	}
	if _, err := BuildCitations(answer.Claims, rendered, bundle,
		func(string, string) ([]string, error) { return []string{"javascript:alert(1)"}, nil }, 4); err == nil {
		t.Fatal("unsafe metadata URL was cited")
	}
	if _, err := BuildCitations(answer.Claims, rendered, bundle,
		func(string, string) ([]string, error) { return nil, errors.New("metadata store unavailable") }, 4); err == nil {
		t.Fatal("trusted metadata failure was ignored")
	}
	changed := proto.Clone(bundle).(*pb.EvidenceBundle)
	changed.Snapshot.SnapshotId = "snapshot:other"
	if _, err := BuildCitations(answer.Claims, rendered, changed, trustedURL, 4); err == nil {
		t.Fatal("cross-snapshot citation was built")
	}
}

func TestBuildCitationsRequiresBlobBoundLocatorForMultipleSources(t *testing.T) {
	answer, rendered, bundle := groundedFixture(t)
	item := bundle.Items[0]
	item.SourceRefs = append(item.SourceRefs, &pb.SourceVersionRef{SourceBlobId: "source:two",
		RegulationId: "regulation:one", ProvisionVersionId: "provision:v1"})
	if _, err := BuildCitations(answer.Claims, rendered, bundle, trustedURL, 4); err == nil {
		t.Fatal("citation accepted source refs changed after context rendering")
	}
	rendered, err := BuildContext(context.Background(), bundle, "context:multisource",
		&pb.ContentHash{Sha256: strings.Repeat("b", 64)}, 1000, 2, countBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildCitations(answer.Claims, rendered, bundle, trustedURL, 4); err == nil {
		t.Fatal("flat span was attributed to multiple blobs")
	}
	item.Locators = []*pb.PageLocator{{SourceBlobId: "source:one", PageNumber: 1},
		{SourceBlobId: "source:two", PageNumber: 2}}
	lookup := func(blob, version string) ([]string, error) {
		if blob == "source:two" && version == "provision:v1" {
			return []string{"https://example.org/second.pdf"}, nil
		}
		return trustedURL(blob, version)
	}
	citations, err := BuildCitations(answer.Claims, rendered, bundle, lookup, 4)
	if err != nil || len(citations) != 2 || citations[0].SourceSpan != nil ||
		citations[0].PageLocator.GetSourceBlobId() != "source:one" ||
		citations[1].PageLocator.GetSourceBlobId() != "source:two" ||
		citations[1].SourceUrl != "https://example.org/second.pdf" {
		t.Fatalf("source-bound page citation not built: %v %v", citations, err)
	}
	if _, err := BuildCitations(answer.Claims, rendered, bundle, lookup, 1); err == nil {
		t.Fatal("expanded source citations exceeded cap without error")
	}
	answer.Citations = citations
	if err := ValidateGroundedAnswer(answer, rendered, bundle, lookup); err != nil {
		t.Fatalf("source-bound page citation failed final gate: %v", err)
	}
	answer.Citations = citations[:1]
	if err := ValidateGroundedAnswer(answer, rendered, bundle, lookup); err == nil {
		t.Fatal("final gate accepted a multi-source claim with one source citation")
	}
}

func TestGroundedAnswerRequiresCitationForEveryClaimEvidence(t *testing.T) {
	answer, rendered, bundle := groundedFixture(t)
	answer.Claims[0].EvidenceIds = []string{"evidence:one", "evidence:two"}
	if err := ValidateGroundedAnswer(answer, rendered, bundle, trustedURL); err == nil {
		t.Fatal("one citation covered two required evidence items")
	}
	citations, err := BuildCitations(answer.Claims, rendered, bundle, trustedURL, 4)
	if err != nil || len(citations) != 2 {
		t.Fatalf("all evidence citations not built: %v %v", citations, err)
	}
	answer.Citations = citations
	lookupCalls := 0
	lookup := func(blob, version string) ([]string, error) {
		lookupCalls++
		return trustedURL(blob, version)
	}
	if err := ValidateGroundedAnswer(answer, rendered, bundle, lookup); err != nil {
		t.Fatalf("fully cited multi-evidence claim rejected: %v", err)
	}
	if lookupCalls != 1 {
		t.Fatalf("final citation gate repeated metadata lookup %d times", lookupCalls)
	}
	unknown := proto.Clone(answer).(*pb.Answer)
	unknown.Citations[0].ClaimIds = []string{"claim:unknown"}
	if err := ValidateGroundedAnswer(unknown, rendered, bundle, lookup); err == nil {
		t.Fatal("citation to unknown claim was accepted")
	}
	duplicate := proto.Clone(answer).(*pb.Answer)
	duplicate.Citations[1].CitationId = duplicate.Citations[0].CitationId
	if err := ValidateGroundedAnswer(duplicate, rendered, bundle, lookup); err == nil {
		t.Fatal("duplicate citation ID was accepted")
	}
	userinfo := proto.Clone(answer).(*pb.Answer)
	userinfo.Citations[0].SourceUrl = "https://official.gov@other.host/file.pdf"
	if err := ValidateGroundedAnswer(userinfo, rendered, bundle,
		func(string, string) ([]string, error) {
			return []string{"https://official.gov@other.host/file.pdf"}, nil
		}); err == nil {
		t.Fatal("URL with userinfo passed the final citation gate")
	}
}
