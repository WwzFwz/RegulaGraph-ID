// Validates advisory RESOLVE proposals against the exact revision-pinned candidate artifact.
// Go must run this before asking the registry to decide assignments: a model-selected LINK
// cannot name an unseen canonical ID, and DEFER cannot silently hide ambiguous candidates.
// This pure boundary does not authenticate PostgreSQL receipts or determine legal correctness.
// Work is bounded by the caller's candidate limits; measure validation p95/p99 and candidate
// coverage on gold under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package domain

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ValidateResolutionProposalsAgainstCandidates requires exactly one LINK/DEFER proposal for
// every extracted mention. The candidate artifact itself is validated before indexing it.
// Registry receipt, current revision CAS, and final decision verification remain workflow work.
func ValidateResolutionProposalsAgainstCandidates(source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef,
	candidates *pb.RegistryCandidateBatch, proposals []*pb.ResolutionProposal,
	maximumReferences, maximumCandidatesPerMention int) error {
	if source == nil || sourceRef == nil || candidates == nil || maximumReferences <= 0 ||
		maximumCandidatesPerMention <= 0 || proto.Size(source) > DefaultWireLimits.MaxBytes ||
		proto.Size(candidates) > DefaultWireLimits.MaxBytes || proto.Size(sourceRef) > DefaultWireLimits.MaxBytes {
		return errors.New("bounded source and candidate artifacts are required")
	}
	if err := ValidateRegistryCandidateBatch(candidates, source, sourceRef, maximumReferences, maximumCandidatesPerMention); err != nil {
		return fmt.Errorf("invalid pinned candidate batch: %w", err)
	}
	if len(proposals) != len(source.Mentions) || len(proposals) > maximumReferences {
		return errors.New("resolution proposals do not cover every source mention")
	}
	mentions := make(map[string]*pb.Mention, len(source.Mentions))
	reservedIDs := map[string]bool{source.GetMeta().GetRecordId(): true, candidates.GetMeta().GetRecordId(): true,
		sourceRef.GetArtifactId(): true}
	for _, mention := range source.Mentions {
		mentions[mention.GetMeta().GetRecordId()] = mention
		reservedIDs[mention.GetMeta().GetRecordId()] = true
	}
	for _, assertion := range source.Assertions {
		reservedIDs[assertion.GetMeta().GetRecordId()] = true
	}
	for _, support := range source.Supports {
		reservedIDs[support.GetMeta().GetRecordId()] = true
	}
	lookups := make(map[string]map[string]bool, len(candidates.Lookups))
	for _, lookup := range candidates.Lookups {
		ids := make(map[string]bool)
		for _, scope := range lookup.Scopes {
			for _, id := range scope.CandidateIds {
				ids[id] = true
			}
		}
		lookups[lookup.MentionId] = ids
	}
	seenMentions := make(map[string]bool, len(proposals))
	seenProposals := make(map[string]bool, len(proposals))
	remainingBytes := DefaultWireLimits.MaxBytes
	for _, proposal := range proposals {
		if proposal == nil {
			return errors.New("nil resolution proposal")
		}
		proposalBytes := proto.Size(proposal)
		if proposalBytes > remainingBytes {
			return errors.New("resolution proposal bytes exceed the wire budget")
		}
		remainingBytes -= proposalBytes
		if proposal.Meta == nil || len(proposal.MentionIds) != 1 ||
			proposal.Meta.CorpusId != source.Meta.CorpusId || proposal.Meta.SchemaVersion != source.Meta.SchemaVersion ||
			proposal.Meta.RecordId == "" || seenProposals[proposal.Meta.RecordId] || reservedIDs[proposal.Meta.RecordId] ||
			proposal.Meta.Visibility != nil || len(proposal.ProposedIdentityKeys) != 0 ||
			proposal.ExpectedRegistryRevision != candidates.RegistryRevision || proposal.Method == "" ||
			proposal.LocalCorrelationId == "" || proposal.Evidence == nil ||
			len(proposal.Evidence.Locators) != 0 {
			return errors.New("resolution proposal identity, revision, or evidence is invalid")
		}
		if err := ValidateWire(proposal, DefaultWireLimits); err != nil {
			return fmt.Errorf("resolution proposal wire is invalid: %w", err)
		}
		seenProposals[proposal.Meta.RecordId] = true
		mentionID := proposal.MentionIds[0]
		mention := mentions[mentionID]
		if mention == nil || seenMentions[mentionID] || mention.TextSpan == nil ||
			len(proposal.Evidence.Spans) != 1 || !proto.Equal(proposal.Evidence.Spans[0], mention.TextSpan) ||
			len(proposal.Evidence.Sources) != len(mention.SourceRefs) {
			return fmt.Errorf("resolution proposal lacks exact source evidence for mention %q", mentionID)
		}
		seenMentions[mentionID] = true
		sourceRefs := make(map[string]int, len(mention.SourceRefs))
		for _, reference := range mention.SourceRefs {
			sourceRefs[sourceRefKey(reference)]++
		}
		for _, reference := range proposal.Evidence.Sources {
			key := sourceRefKey(reference)
			if key == "\x00\x00" || sourceRefs[key] == 0 {
				return fmt.Errorf("resolution proposal invents source version for mention %q", mentionID)
			}
			sourceRefs[key]--
		}
		allowed := lookups[mentionID]
		switch proposal.Action {
		case pb.ResolutionAction_RESOLUTION_ACTION_LINK:
			if len(proposal.CandidateIds) != 1 || !allowed[proposal.CandidateIds[0]] {
				return fmt.Errorf("LINK target is absent from pinned lookup for mention %q", mentionID)
			}
		case pb.ResolutionAction_RESOLUTION_ACTION_DEFER:
			if len(proposal.CandidateIds) != len(allowed) {
				return fmt.Errorf("DEFER hides candidate IDs for mention %q", mentionID)
			}
			seen := make(map[string]bool, len(proposal.CandidateIds))
			for _, id := range proposal.CandidateIds {
				if !allowed[id] || seen[id] {
					return fmt.Errorf("DEFER changes candidate IDs for mention %q", mentionID)
				}
				seen[id] = true
			}
		default:
			return fmt.Errorf("proposal action %s is not supported by candidate handoff", proposal.Action)
		}
	}
	return nil
}
