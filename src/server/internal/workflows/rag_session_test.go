// Verifies the snapshot lease spans search/hydration/generation and is released
// on every admitted exit. Storage/model doubles exercise failure and cancellation
// ownership; live store hydration is covered by initial_writer_test.go, not by
// these tests. They do not establish production latency or model quality.
package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type ragLeaseStore struct {
	index                 *domain.PinnedIndex
	pins, reads, releases int
	failRead              int
	releaseErr            error
	mutate                func(*domain.PinnedIndex)
}

func (s *ragLeaseStore) PinActiveSnapshot(_ context.Context, corpus, lease, owner string, ttl time.Duration) (domain.SnapshotPin, error) {
	s.pins++
	s.index.Pin = domain.SnapshotPin{LeaseID: lease, OwnerID: owner, CorpusID: corpus, SnapshotID: s.index.Snapshot.SnapshotId, Sequence: s.index.Snapshot.Sequence, ExpiresAt: time.Now().Add(ttl)}
	return s.index.Pin, nil
}
func (s *ragLeaseStore) LoadPinnedIndex(ctx context.Context, pin domain.SnapshotPin) (*domain.PinnedIndex, error) {
	s.reads++
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s.reads == s.failRead {
		return nil, domain.ErrLeaseUnavailable
	}
	if pin != s.index.Pin || s.releases > 0 {
		return nil, errors.New("lost lease")
	}
	out := *s.index
	out.Snapshot = proto.Clone(s.index.Snapshot).(*pb.SnapshotRef)
	if s.mutate != nil {
		s.mutate(&out)
	}
	return &out, nil
}
func (s *ragLeaseStore) ReleaseSnapshotPin(ctx context.Context, lease, owner string) error {
	s.releases++
	if ctx.Err() != nil || lease != s.index.Pin.LeaseID || owner != s.index.Pin.OwnerID {
		return errors.New("cleanup lost ownership or context")
	}
	return s.releaseErr
}

func sessionGeneration() *pb.IndexGeneration {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	artifact := &pb.ArtifactRef{ArtifactId: "artifact:one", ContentHash: hash, StorageKey: "objects/a", MediaType: "application/x-protobuf", SchemaVersion: 1}
	return &pb.IndexGeneration{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "generation:one"},
		DenseManifest:   &pb.ModelManifest{ModelId: "model:one", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_EMBED, Dimensions: proto.Uint32(2), MaxTokens: 512, Precision: "fp32", Backend: "fixture"},
		LexicalAnalyzer: artifact, LexicalDictionary: artifact, LexicalStatistics: artifact, OntologyVersion: "ontology:v1", FilterFormat: pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1, EmbeddingInputPolicy: "structure-labels-v1"}
}

func TestRAGSessionOwnsLeaseThroughDraft(t *testing.T) {
	for _, mode := range []string{"success", "factory failure", "lease revoked", "cleanup failure", "cancelled generation", "historical mismatch", "malformed catalog", "missing date", "unauthorized corpus"} {
		t.Run(mode, func(t *testing.T) {
			w, request, input, provider := ragFixture(t)
			store := &ragLeaseStore{index: &domain.PinnedIndex{Snapshot: proto.Clone(input.Context.SnapshotRef).(*pb.SnapshotRef), Binding: domain.IndexCatalogBinding{PublicationID: "publication:one", Fence: 1, Endpoint: "http://fixture", Collection: "fixture", Generation: sessionGeneration()}}}
			call := proto.Clone(input.Context).(*pb.RequestContext)
			call.SnapshotRef = nil
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			factoryCalls := 0
			factory := func(_ context.Context, index *domain.PinnedIndex) (*RAGWorkflow, error) {
				factoryCalls++
				if mode == "factory failure" {
					return nil, errors.New("factory failure")
				}
				if index.Pin != store.index.Pin {
					t.Fatal("factory lost pin")
				}
				return w, nil
			}
			hydrate := w.Hydrate
			w.Hydrate = func(c context.Context, r *pb.QuestionRequest, found *CandidateSearchResult) (*HydratedCandidates, error) {
				if store.releases != 0 || !proto.Equal(found.Snapshot, store.index.Snapshot) {
					t.Fatal("hydration not protected by pin")
				}
				if deadline, ok := c.Deadline(); !ok || deadline.After(store.index.Pin.ExpiresAt) {
					t.Fatal("request outlives lease")
				}
				h, e := hydrate(c, r, found)
				if mode == "cancelled generation" {
					cancel()
				}
				return h, e
			}
			switch mode {
			case "lease revoked":
				store.failRead = 2
			case "cleanup failure":
				store.releaseErr = errors.New("release failed")
			case "historical mismatch":
				request.SnapshotId = proto.String("snapshot:old")
			case "malformed catalog":
				store.mutate = func(i *domain.PinnedIndex) { i.Binding.Generation = nil }
			case "missing date":
				request.TemporalScope.EffectiveAt = nil
			case "unauthorized corpus":
				call.CorpusId = "corpus:foreign"
			}
			before := proto.Clone(call)
			session := &RAGSession{Store: store, Factory: factory, OwnerID: "reader:test", MaximumDuration: time.Second * 20, SearchLimit: 10}
			result, err := session.AnswerQuestion(ctx, request, call)
			if mode == "success" {
				if err != nil || result == nil || provider.calls != 1 || len(result.Answer.Draft.Answer.Citations) != 1 {
					t.Fatalf("draft=%v err=%v model calls=%d", result, err, provider.calls)
				}
			} else if err == nil || result != nil {
				t.Fatal("invalid session returned an answer", mode)
			}
			if !proto.Equal(before, call) {
				t.Fatal("mutated caller context")
			}
			if store.pins != store.releases {
				t.Fatalf("pins=%d releases=%d", store.pins, store.releases)
			}
			if mode == "missing date" || mode == "unauthorized corpus" {
				if store.pins != 0 {
					t.Fatal("inadmissible request acquired lease")
				}
			}
			if mode == "historical mismatch" || mode == "malformed catalog" {
				if factoryCalls != 0 || provider.calls != 0 {
					t.Fatal("invalid snapshot reached factory/model")
				}
			}
		})
	}
}
