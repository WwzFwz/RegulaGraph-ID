// Verifies restart replay, input/producer isolation and fail-closed persistence.
// Recreated services have empty process caches; provider doubles cannot establish
// actual model quality. The PostgreSQL opt-in test covers durable storage bytes.
package inference

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type completionMemoryStore struct {
	mu            sync.Mutex
	values        map[string]domain.ModelCompletion
	failLoad      bool
	failAfterSave bool
}

func (m *completionMemoryStore) LoadModelCompletion(_ context.Context, key *pb.ContentHash) (domain.ModelCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failLoad {
		return domain.ModelCompletion{}, errors.New("injected storage read failure")
	}
	v, ok := m.values[key.Sha256]
	if !ok {
		return v, domain.ErrNotFound
	}
	v.JSON = append([]byte(nil), v.JSON...)
	return v, nil
}
func (m *completionMemoryStore) SaveModelCompletion(ctx context.Context, key *pb.ContentHash, v domain.ModelCompletion) (domain.ModelCompletion, error) {
	m.mu.Lock()
	if _, ok := m.values[key.Sha256]; !ok {
		v.JSON = append([]byte(nil), v.JSON...)
		m.values[key.Sha256] = v
	}
	fail := m.failAfterSave
	m.mu.Unlock()
	if fail {
		return domain.ModelCompletion{}, errors.New("injected lost commit acknowledgement")
	}
	return m.LoadModelCompletion(ctx, key)
}

func newReplayService(t *testing.T, p StructuredProvider, cfg SemanticConfig, store domain.ModelCompletionStore) *SemanticService {
	t.Helper()
	cfg.ExtractionStore = store
	s, err := NewSemanticService(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExtractCompletionReplayAcrossServiceRestart(t *testing.T) {
	store := &completionMemoryStore{values: map[string]domain.ModelCompletion{}}
	p := &providerDouble{raw: validRawProposal()}
	base, request := semanticFixture(p)
	first := newReplayService(t, p, base.config, store)
	a, err := first.ExtractBatch(context.Background(), request)
	if err != nil || a.Results[0].GetProposal() == nil {
		t.Fatalf("initial extract failed: %v", err)
	}
	p2 := &providerDouble{err: errors.New("provider must not run on replay")}
	restarted := newReplayService(t, p2, base.config, store)
	retry := proto.Clone(request).(*pb.ExtractBatchRequest)
	retry.Batch.Context.RequestId = "request:after-restart"
	retry.Batch.Context.TraceId = "trace:after-restart"
	retry.Batch.Context.Deadline = timestamppb.New(time.Now().Add(time.Minute))
	b, err := restarted.ExtractBatch(context.Background(), retry)
	if err != nil || !proto.Equal(a.Results[0], b.Results[0]) || p2.callCount() != 0 || b.RequestId != retry.Batch.Context.RequestId {
		t.Fatalf("restart did not replay: %v", err)
	}
	for _, mutate := range []func(*pb.ExtractBatchRequest){
		func(r *pb.ExtractBatchRequest) { r.Batch.Context.AuthScopeRef = "scope:other" },
		func(r *pb.ExtractBatchRequest) { r.Items[0].Text = "Bidan wajib izin." },
		func(r *pb.ExtractBatchRequest) { r.Items[0].Provenance.Spans[0].TextArtifactId = "text:other" },
	} {
		changed := proto.Clone(request).(*pb.ExtractBatchRequest)
		mutate(changed)
		probe := &providerDouble{raw: validRawProposal()}
		s := newReplayService(t, probe, base.config, store)
		if _, err = s.ExtractBatch(context.Background(), changed); err != nil {
			t.Fatal(err)
		}
		if probe.callCount() != 1 {
			t.Fatal("changed scope/source reused completion")
		}
	}
	changedConfig := base.config
	changedConfig.ConfigHash = semanticHash("f")
	probe := &providerDouble{raw: validRawProposal()}
	s := newReplayService(t, probe, changedConfig, store)
	if _, err = s.ExtractBatch(context.Background(), request); err != nil || probe.callCount() != 1 {
		t.Fatalf("producer drift reused completion: %v", err)
	}
}

func TestExtractReplayRequiresStorageAndRevalidatesOutput(t *testing.T) {
	store := &completionMemoryStore{values: map[string]domain.ModelCompletion{}, failLoad: true}
	p := &providerDouble{raw: validRawProposal()}
	base, request := semanticFixture(p)
	s := newReplayService(t, p, base.config, store)
	a, err := s.ExtractBatch(context.Background(), request)
	if err != nil || !a.Results[0].GetError().GetRetryable() || p.callCount() != 0 {
		t.Fatalf("storage failure silently bypassed: %v", err)
	}
	store.failLoad = false
	store.failAfterSave = true
	a, err = s.ExtractBatch(context.Background(), request)
	if err != nil || !a.Results[0].GetError().GetRetryable() || p.callCount() != 1 {
		t.Fatalf("lost ack not transient: %v", err)
	}
	store.failAfterSave = false
	p2 := &providerDouble{err: errors.New("must replay committed output")}
	s = newReplayService(t, p2, base.config, store)
	a, err = s.ExtractBatch(context.Background(), request)
	if err != nil || a.Results[0].GetProposal() == nil || p2.callCount() != 0 {
		t.Fatalf("lost-ack restart resampled: %v", err)
	}
	// Storage returns valid JSON that is semantically invalid: no bypass of
	// projection/ontology validation is permitted after replay.
	for k, v := range store.values {
		v.JSON = []byte(`{"mentions":[],"assertions":[]}`)
		store.values[k] = v
	}
	s = newReplayService(t, p2, base.config, store)
	a, err = s.ExtractBatch(context.Background(), request)
	if err != nil || a.Results[0].GetError() == nil || p2.callCount() != 0 {
		t.Fatalf("replay skipped semantic validation: %v", err)
	}
}
