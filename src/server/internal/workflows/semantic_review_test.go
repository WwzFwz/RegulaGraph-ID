// Exercises operator review against actual proposal/receipt builders and a deterministic
// model fixture. Changed hashes, scopes, manifests, reason and corrupt bytes cannot reach
// the atomic store. This proves boundary behavior, not legal correctness or model quality.
package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type reviewTestStore struct {
	*modelProposalStore
	queue    domain.SemanticReviewQueue
	accepted *domain.SemanticReviewCommit
	calls    int
}

func (s *reviewTestStore) LoadSemanticReviewQueue(_ context.Context, corpus, job string) (domain.SemanticReviewQueue, error) {
	if corpus != s.queue.CorpusID || job != s.queue.JobID {
		return domain.SemanticReviewQueue{}, domain.ErrNotFound
	}
	return s.queue, nil
}
func (s *reviewTestStore) AcceptSemanticReview(_ context.Context, review domain.SemanticReviewCommit) (bool, error) {
	s.calls++
	s.accepted = &review
	return true, nil
}

func reviewFixture(t *testing.T) (*SemanticReviewer, *reviewTestStore, *modelProposalArtifacts) {
	return reviewFixtureWithLink(t, false)
}

func reviewFixtureWithLink(t *testing.T, link bool) (*SemanticReviewer, *reviewTestStore, *modelProposalArtifacts) {
	t.Helper()
	h, job, candidate, batch, producer, gateway, provider, store, artifacts := modelWorkflowFixture(t)
	if link {
		candidate, _ = candidateEvidenceFixture(t, store, artifacts)
		provider.raw = json.RawMessage(candidateLinkJSON)
	}
	proposed, err := h.ProposeWithModel(context.Background(), job, candidate, batch, producer, gateway)
	if err != nil {
		t.Fatal(err)
	}
	pin := parseHash("e")
	store.request = &pb.IngestionRequest{CorpusId: job.CorpusID, ConfigManifest: &pb.ProducerManifest{ConfigHash: batch.Context.ConfigFingerprint, InputHashes: []*pb.ContentHash{pin}}}
	s := &reviewTestStore{modelProposalStore: store, queue: domain.SemanticReviewQueue{JobID: job.JobID, CorpusID: job.CorpusID, SourceCheckpointID: store.checkpoint.Meta.RecordId, Source: store.refs[store.checkpoint.CompletedBatchKeys[0]], Candidates: candidate, Input: proposed.InputArtifact, Output: proposed.OutputArtifact}}
	r, err := NewSemanticReviewer(s, artifacts, SemanticReviewConfig{Actor: "operator:test", Corpus: job.CorpusID, AuthScope: batch.Context.AuthScopeRef, Producer: producer, ProducerPin: pin, MaximumBytes: 1 << 20, MaximumReferences: 1000, MaximumCandidates: 10})
	if err != nil {
		t.Fatal(err)
	}
	return r, s, artifacts
}

func TestSemanticReviewLinksExactCanonicalAndActor(t *testing.T) {
	r, s, _ := reviewFixtureWithLink(t, true)
	if _, err := r.Accept(context.Background(), s.queue.JobID, s.queue.Output.ContentHash.Sha256, 3, "Checked both source contexts"); err != nil {
		t.Fatal(err)
	}
	if len(s.accepted.Intent.Approvals) != 1 || s.accepted.Intent.Approvals[0].CanonicalID != "canonical:candidate" || s.accepted.Intent.Approvals[0].Actor != "operator:test" {
		t.Fatal("lost LINK authority")
	}
}

func TestSemanticReviewRejectsRehashedInputDrift(t *testing.T) {
	for _, name := range []string{"mention", "candidate", "revision", "support", "coverage"} {
		t.Run(name, func(t *testing.T) {
			r, s, a := reviewFixtureWithLink(t, true)
			input := new(pb.SemanticResolveRequest)
			if err := proto.Unmarshal(a.contents[s.queue.Input.ArtifactId], input); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "mention":
				input.Items[0].Mention.ExtractionManifest.Build = "changed"
			case "candidate":
				input.Items[0].Candidates[0].PreferredLabel = "unverified label"
			case "revision":
				input.Items[0].ExpectedRegistryRevision++
			case "support":
				input.Items[0].CandidateContexts[0].AliasId = "alias:invented"
			case "coverage":
				input.Items = append(input.Items, proto.Clone(input.Items[0]).(*pb.AmbiguousMention))
			}
			raw, _ := proto.Marshal(input)
			ref := semanticRefForTest("artifact:review-rehashed", raw)
			a.contents[ref.ArtifactId] = raw
			s.queue.Input = ref
			if _, err := r.Inspect(context.Background(), s.queue.JobID); err == nil || s.calls != 0 {
				t.Fatal("rehashed input drift accepted", name)
			}
		})
	}
}

func TestSemanticReviewBuildsCompleteIntent(t *testing.T) {
	r, s, _ := reviewFixture(t)
	accepted, err := r.Accept(context.Background(), s.queue.JobID, s.queue.Output.ContentHash.Sha256, 3, "Verified the source; no candidate establishes identity.")
	if err != nil || !accepted || s.calls != 1 {
		t.Fatal(accepted, err)
	}
	intent := s.accepted.Intent
	if len(intent.Approvals) != 0 || len(intent.Preview.Decisions) != 1 || intent.Preview.Decisions[0].Action != pb.ResolutionAction_RESOLUTION_ACTION_DEFER || !proto.Equal(intent.Preview.Proposals[0], intent.Request.Proposals[0]) {
		t.Fatal("review dropped or changed proposal")
	}
	if s.accepted.Actor != "operator:test" || intent.SourceCheckpointID != s.queue.SourceCheckpointID {
		t.Fatal("authority lost")
	}
}

func TestSemanticReviewRejectsDriftBeforeStore(t *testing.T) {
	for _, name := range []string{"hash", "revision", "reason", "scope", "corpus", "producer", "producer-pin", "bytes", "budget"} {
		t.Run(name, func(t *testing.T) {
			r, s, a := reviewFixture(t)
			hash := s.queue.Output.ContentHash.Sha256
			revision := uint64(3)
			reason := "inspected"
			switch name {
			case "hash":
				hash = parseHash("f").Sha256
			case "revision":
				revision++
			case "reason":
				reason = " "
			case "scope":
				r.config.AuthScope = "unauthorized"
			case "corpus":
				r.config.Corpus = "corpus:other"
			case "producer":
				r.config.Producer.Build = "other"
			case "producer-pin":
				r.config.ProducerPin = parseHash("f")
			case "bytes":
				a.contents[s.queue.Output.ArtifactId] = []byte("corrupt")
			case "budget":
				r.config.MaximumBytes = 1
			}
			if _, err := r.Accept(context.Background(), s.queue.JobID, hash, revision, reason); err == nil || s.calls != 0 {
				t.Fatal("unsafe acceptance", err)
			}
		})
	}
}

func TestSemanticReviewMissingQueue(t *testing.T) {
	r, _, _ := reviewFixture(t)
	if _, err := r.Inspect(context.Background(), "job:missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}
