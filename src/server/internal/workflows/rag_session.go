// Owns one active-snapshot read lease for the complete RAG request, including
// retrieval, source hydration and generation. The trusted caller authenticates
// corpus access and supplies request context; a factory reuses model/client
// handles for the pinned generation, never loads models per request. Deadlines
// cannot exceed the lease; cleanup uses a separate bounded context. Measure
// lease overhead and full request p95/p99 under benchmark-targets.yaml; no gold
// or performance acceptance is implied by this orchestration boundary.
package workflows

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
)

type RAGSnapshotStore interface {
	PinActiveSnapshot(context.Context, string, string, string, time.Duration) (domain.SnapshotPin, error)
	LoadPinnedIndex(context.Context, domain.SnapshotPin) (*domain.PinnedIndex, error)
	ReleaseSnapshotPin(context.Context, string, string) error
}
type PinnedRAGFactory func(context.Context, *domain.PinnedIndex) (*RAGWorkflow, error)
type RAGSession struct {
	Store           RAGSnapshotStore
	Factory         PinnedRAGFactory
	OwnerID         string
	MaximumDuration time.Duration
	SearchLimit     int
}

// AnswerQuestion pins active once. Explicit historical requests that differ
// from that snapshot fail; they are never silently redirected to latest data.
// No endpoint should accept the trusted RequestContext directly from JSON.
func (s *RAGSession) AnswerQuestion(ctx context.Context, request *pb.QuestionRequest, call *pb.RequestContext) (result *RAGResult, err error) {
	if ctx == nil || s == nil || s.Store == nil || s.Factory == nil || s.OwnerID == "" || s.MaximumDuration <= 0 || s.SearchLimit <= 0 || s.SearchLimit > 256 || request == nil || call == nil {
		return nil, errors.New("configured RAG session and authorized request context required")
	}
	for _, m := range []proto.Message{request, call} {
		if e := domain.ValidateWire(m, domain.DefaultWireLimits); e != nil {
			return nil, e
		}
	}
	if request.CorpusId != call.CorpusId || call.SnapshotRef != nil || request.TemporalScope == nil || request.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || request.TemporalScope.EffectiveAt == nil || len(request.TemporalScope.CompareDates) != 0 || request.ResponseMode != pb.ResponseMode_RESPONSE_MODE_COMPLETE ||
		(request.RequestedProfile != pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG && request.RequestedProfile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG) {
		return nil, errors.New("RAG session requires authorized corpus, unset snapshot and supported complete AS_OF profile")
	}
	deadline := time.Now().Add(s.MaximumDuration)
	if call.Deadline.AsTime().Before(deadline) {
		deadline = call.Deadline.AsTime()
	}
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	var entropy [16]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	leaseID := "rag-read:" + hex.EncodeToString(entropy[:])
	pin, err := s.Store.PinActiveSnapshot(bounded, call.CorpusId, leaseID, s.OwnerID, time.Until(deadline))
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if e := s.Store.ReleaseSnapshotPin(cleanup, leaseID, s.OwnerID); e != nil {
			result = nil
			err = errors.Join(err, e)
		}
	}()
	if pin.CorpusID != call.CorpusId || pin.LeaseID != leaseID || pin.OwnerID != s.OwnerID {
		return nil, errors.New("snapshot store returned a different lease")
	}
	if pin.ExpiresAt.Before(deadline) {
		deadline = pin.ExpiresAt
	}
	leased, releaseContext := context.WithDeadline(bounded, deadline)
	defer releaseContext()
	index, err := s.Store.LoadPinnedIndex(leased, pin)
	if err != nil {
		return nil, err
	}
	if index == nil || index.Snapshot == nil || index.Pin != pin || index.Snapshot.CorpusId != call.CorpusId || index.Snapshot.SnapshotId != pin.SnapshotID || index.Snapshot.Sequence != pin.Sequence {
		return nil, errors.New("pinned index admission mismatch")
	}
	if err = domain.ValidateWire(index.Snapshot, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if err = domain.ValidateIndexCatalogBinding(index.Binding); err != nil {
		return nil, err
	}
	if index.Binding.Generation.Meta.CorpusId != call.CorpusId || index.Binding.Generation.Meta.RecordId != index.Snapshot.RepresentationGeneration {
		return nil, errors.New("pinned index generation mismatch")
	}
	if request.SnapshotId != nil && *request.SnapshotId != index.Snapshot.SnapshotId || request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, index.Snapshot) {
		return nil, errors.New("requested historical snapshot is not the admitted active snapshot")
	}
	owned := proto.Clone(call).(*pb.RequestContext)
	owned.SnapshotRef = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
	owned.Deadline = timestamppb.New(deadline)
	generation := proto.Clone(index.Binding.Generation).(*pb.IndexGeneration)
	workflow, err := s.Factory(leased, index)
	if err != nil {
		return nil, err
	}
	result, err = workflow.AnswerPinnedQuestion(leased, request, retrieval.SearchInput{Context: owned, Question: request.Question, Generation: generation, Scope: qdrant.SearchScope{SnapshotSeq: pin.Sequence, Limit: s.SearchLimit}})
	if err != nil {
		return nil, err
	}
	if _, err = s.Store.LoadPinnedIndex(leased, pin); err != nil {
		return nil, err
	}
	if err = leased.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
