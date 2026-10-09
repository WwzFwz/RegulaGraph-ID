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
	"regulagraph.local/server/internal/retrieval/query"
)

type RAGSnapshotStore interface {
	PinActiveSnapshot(context.Context, string, string, string, time.Duration) (domain.SnapshotPin, error)
	LoadPinnedIndex(context.Context, domain.SnapshotPin) (*domain.PinnedIndex, error)
	ReleaseSnapshotPin(context.Context, string, string) error
}
type PinnedRAGFactory func(context.Context, *domain.PinnedIndex) (*RAGWorkflow, error)
type RAGSession struct {
	TimeZone        *time.Location
	Clock           func() time.Time // Trusted injectable clock; defaults to time.Now.
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
	return s.runPinned(ctx, request, call, true)
}

// SearchQuestion owns the same lease and admission as answering, but returns
// evidence only. It never converts a generator failure into a search success.
func (s *RAGSession) SearchQuestion(ctx context.Context, request *pb.QuestionRequest, call *pb.RequestContext) (*RAGResult, error) {
	return s.runPinned(ctx, request, call, false)
}

func (s *RAGSession) runPinned(ctx context.Context, request *pb.QuestionRequest, call *pb.RequestContext, generate bool) (*RAGResult, error) {
	if s == nil || request == nil || call == nil {
		return nil, errors.New("configured session and question required")
	}
	// Reject oversized/malformed library inputs before cloning or sampling the
	// calendar clock. The shared pin helper also validates the resolved request.
	for _, message := range []proto.Message{request, call} {
		if err := domain.ValidateWire(message, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	clock := s.Clock
	if clock == nil {
		clock = time.Now
	}
	scope, temporal, err := query.ResolveTemporalScope(request.TemporalScope, s.TimeZone, clock)
	if err != nil {
		return nil, err
	}
	owned := proto.Clone(request).(*pb.QuestionRequest)
	owned.TemporalScope = scope
	return withRAGSnapshot(ctx, s, owned, call, func(c context.Context, workflow *RAGWorkflow, request *pb.QuestionRequest, input retrieval.SearchInput) (*RAGResult, error) {
		var result *RAGResult
		var err error
		if generate {
			result, err = workflow.AnswerPinnedQuestion(c, request, input)
		} else {
			result, err = workflow.SearchPinnedQuestion(c, request, input)
		}
		if err != nil {
			return nil, err
		}
		result.Temporal = temporal
		return result, nil
	})
}

// withRAGSnapshot owns one lease and one resource factory for the entire operation.
// T is either a single-date result or a date comparison; all errors discard output.
func withRAGSnapshot[T any](ctx context.Context, s *RAGSession, request *pb.QuestionRequest, call *pb.RequestContext, execute func(context.Context, *RAGWorkflow, *pb.QuestionRequest, retrieval.SearchInput) (T, error)) (result T, err error) {
	var zero T
	if ctx == nil || s == nil || s.Store == nil || s.Factory == nil || s.OwnerID == "" || s.MaximumDuration <= 0 || s.SearchLimit <= 0 || s.SearchLimit > 256 || request == nil || call == nil {
		return zero, errors.New("configured RAG session and authorized request context required")
	}
	for _, m := range []proto.Message{request, call} {
		if e := domain.ValidateWire(m, domain.DefaultWireLimits); e != nil {
			return zero, e
		}
	}
	if request.CorpusId != call.CorpusId || call.SnapshotRef != nil || request.ResponseMode != pb.ResponseMode_RESPONSE_MODE_COMPLETE ||
		(request.RequestedProfile != pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG && request.RequestedProfile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG && request.RequestedProfile != pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG && request.RequestedProfile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG) {
		return zero, errors.New("RAG session requires authorized corpus, unset snapshot and supported complete retrieval profile")
	}
	request = proto.Clone(request).(*pb.QuestionRequest)
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
		return zero, err
	}
	var entropy [16]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return zero, err
	}
	leaseID := "rag-read:" + hex.EncodeToString(entropy[:])
	pin, err := s.Store.PinActiveSnapshot(bounded, call.CorpusId, leaseID, s.OwnerID, time.Until(deadline))
	if err != nil {
		return zero, err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if e := s.Store.ReleaseSnapshotPin(cleanup, leaseID, s.OwnerID); e != nil {
			result = zero
			err = errors.Join(err, e)
		}
	}()
	if pin.CorpusID != call.CorpusId || pin.LeaseID != leaseID || pin.OwnerID != s.OwnerID {
		return zero, errors.New("snapshot store returned a different lease")
	}
	if pin.ExpiresAt.Before(deadline) {
		deadline = pin.ExpiresAt
	}
	leased, releaseContext := context.WithDeadline(bounded, deadline)
	defer releaseContext()
	index, err := s.Store.LoadPinnedIndex(leased, pin)
	if err != nil {
		return zero, err
	}
	if index == nil || index.Snapshot == nil || index.Pin != pin || index.Snapshot.CorpusId != call.CorpusId || index.Snapshot.SnapshotId != pin.SnapshotID || index.Snapshot.Sequence != pin.Sequence {
		return zero, errors.New("pinned index admission mismatch")
	}
	if err = domain.ValidateWire(index.Snapshot, domain.DefaultWireLimits); err != nil {
		return zero, err
	}
	if err = domain.ValidateIndexCatalogBinding(index.Binding); err != nil {
		return zero, err
	}
	if index.Binding.Generation.Meta.CorpusId != call.CorpusId || index.Binding.Generation.Meta.RecordId != index.Snapshot.RepresentationGeneration {
		return zero, errors.New("pinned index generation mismatch")
	}
	if request.SnapshotId != nil && *request.SnapshotId != index.Snapshot.SnapshotId || request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, index.Snapshot) {
		return zero, errors.New("requested historical snapshot is not the admitted active snapshot")
	}
	owned := proto.Clone(call).(*pb.RequestContext)
	owned.SnapshotRef = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
	owned.Deadline = timestamppb.New(deadline)
	generation := proto.Clone(index.Binding.Generation).(*pb.IndexGeneration)
	workflow, err := s.Factory(leased, index)
	if err != nil {
		return zero, err
	}
	input := retrieval.SearchInput{Context: owned, Question: request.Question, Generation: generation, Scope: qdrant.SearchScope{SnapshotSeq: pin.Sequence, Limit: s.SearchLimit}}
	result, err = execute(leased, workflow, request, input)
	if err != nil {
		return zero, err
	}
	if _, err = s.Store.LoadPinnedIndex(leased, pin); err != nil {
		return zero, err
	}
	if err = leased.Err(); err != nil {
		return zero, err
	}
	return result, nil
}
