// Exercises the actual RESOLVE planner/hydration/model workflow under a durable executor.
// Synthetic fixtures verify parking, crash replay, configuration drift, cancellation and
// bounded retry without claiming legal accuracy or required latency. PostgreSQL queue
// atomicity is covered separately against an opt-in disposable database.
package workflows

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/domain"
	"sync/atomic"
	"testing"
	"time"
)

type executorStoreFake struct {
	*modelProposalStore
	job       domain.JobRecord
	parked    *pb.ArtifactRef
	parkErr   error
	finished  pb.JobState
	retry     time.Duration
	cancelled atomic.Bool
	intent    *domain.SemanticResolutionIntent
}

func (s *executorStoreFake) ClaimResolveJob(context.Context, string, time.Duration) (domain.JobRecord, error) {
	s.job.LeaseFence++
	s.job.Attempt++
	s.job.StageAttempt++
	return s.job, nil
}
func (s *executorStoreFake) CancellationRequested(context.Context, string, string, uint64) (bool, error) {
	return s.cancelled.Load(), nil
}
func (s *executorStoreFake) CompleteWorkerAttempt(_ context.Context, _, _ string, _ uint64, state pb.JobState, delay time.Duration) (pb.JobState, error) {
	if s.cancelled.Load() {
		state = pb.JobState_JOB_STATE_CANCELLED
	}
	s.finished = state
	s.retry = delay
	return state, nil
}
func (s *executorStoreFake) ParkSemanticProposal(_ context.Context, _ domain.SemanticJobFence, _ *pb.ArtifactRef, _ *pb.ExtractionBatch, _, _, output *pb.ArtifactRef) (pb.JobState, error) {
	if s.parkErr != nil {
		return pb.JobState_JOB_STATE_UNSPECIFIED, s.parkErr
	}
	if s.cancelled.Load() {
		return pb.JobState_JOB_STATE_CANCELLED, nil
	}
	s.parked = proto.Clone(output).(*pb.ArtifactRef)
	return pb.JobState_JOB_STATE_WAITING_REVIEW, nil
}
func (s *executorStoreFake) LoadSemanticResolutionIntent(context.Context, string, string) (domain.SemanticResolutionIntent, error) {
	if s.intent != nil {
		return *s.intent, nil
	}
	return domain.SemanticResolutionIntent{}, domain.ErrNotFound
}
func (s *executorStoreFake) SaveSemanticResolutionIntent(context.Context, domain.SemanticJobFence, *pb.ArtifactRef, *pb.ExtractionBatch, domain.SemanticResolutionIntent) error {
	return errors.New("unexpected decision intent")
}
func (s *executorStoreFake) SaveCheckpoint(_ context.Context, c *pb.Checkpoint, _ string) error {
	s.checkpoint = c
	return nil
}
func (s *executorStoreFake) ReadEmptyResolveRevision(context.Context, domain.SemanticJobFence, *pb.ArtifactRef, *pb.ExtractionBatch) (uint64, error) {
	return s.revision, nil
}
func (s *executorStoreFake) PrepareRegistryCandidateBatch(_ context.Context, source *pb.ExtractionBatch, ref *pb.ArtifactRef, producer *pb.ProducerManifest, id string,
	plans []domain.RegistryCandidatePlan, _, _, references, candidates int) (*pb.RegistryCandidateBatch, error) {
	lookups := make([]*pb.CandidateLookup, 0, len(plans))
	for _, plan := range plans {
		lookup := &pb.CandidateLookup{MentionId: plan.MentionID}
		for _, scope := range plan.Scopes {
			lookup.Scopes = append(lookup.Scopes, &pb.CandidateLookupScope{
				EntityType: scope.EntityType, CanonicalScope: scope.CanonicalScope, NormalizedLookup: scope.NormalizedLookup,
				Revision: &pb.LookupScopeRevision{ScopeId: domain.RegistryLookupScopeID(scope.EntityType, scope.CanonicalScope, scope.NormalizedLookup), EmptyResult: true}})
		}
		lookups = append(lookups, lookup)
	}
	return domain.AssembleRegistryCandidateBatch(source, ref, producer, id, s.revision, lookups, nil, nil, references, candidates)
}

func executorFixture(t *testing.T) (*SemanticExecutor, *executorStoreFake, *modelProposalArtifacts, *modelProposalProvider, *inference.SemanticService) {
	t.Helper()
	_, job, _, batch, producer, gateway, provider, base, artifacts := modelWorkflowFixture(t)
	sourceRef := base.refs[base.checkpoint.CompletedBatchKeys[0]]
	source := new(pb.ExtractionBatch)
	if err := proto.Unmarshal(artifacts.contents[sourceRef.ArtifactId], source); err != nil {
		t.Fatal(err)
	}
	base.checkpoint.Manifest = proto.Clone(source.Dependencies.ProducerManifest).(*pb.ProducerManifest)
	policy := domain.CandidatePlanningPolicy{ScopesByType: map[string][]string{"permit": {"national"}}, MaximumMentions: 8, MaximumScopesPerMention: 2, MaximumTotalScopes: 16}
	policyHash, _ := policy.Fingerprint()
	pin := parseHash("d")
	base.request = &pb.IngestionRequest{CorpusId: job.CorpusID, Sources: []*pb.SourceLocator{{PortalId: "bpk", Locator: &pb.SourceLocator_Blob{Blob: sourceRef}}},
		Operation: pb.JobOperation_JOB_OPERATION_INGEST, IdempotencyKey: "executor:fixture", ConfigManifest: &pb.ProducerManifest{
			Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: source.Context.ConfigFingerprint,
			InputHashes: []*pb.ContentHash{policyHash, pin, parseTestOntology().ContentHash()}}}
	store := &executorStoreFake{modelProposalStore: base, job: job}
	if err := domain.ValidateWire(base.request, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	config := SemanticExecutorConfig{OwnerID: job.LeaseOwner, AuthScope: source.Context.AuthScopeRef, Lease: time.Minute, CallTimeout: 30 * time.Second,
		CancellationPoll: time.Millisecond, RetryBase: time.Second, RetryMax: time.Minute, Ontology: parseTestOntology(),
		Policies: map[string]domain.CandidatePlanningPolicy{job.CorpusID: policy}, Producer: producer, ProducerPin: pin, OutputSchema: batch.OutputSchema,
		MaximumBytes: 1 << 20, MaximumReferences: 1000, MaximumCandidatesPerMention: 10, MaximumScopes: 16, MaximumAliasesPerScope: 16}
	executor, err := NewSemanticExecutor(store, artifacts, gateway, config)
	if err != nil {
		t.Fatal(err)
	}
	return executor, store, artifacts, provider, gateway
}

func TestSemanticExecutorParksAndReplaysAfterCrash(t *testing.T) {
	e, store, _, provider, _ := executorFixture(t)
	store.parkErr = errors.New("injected crash after model artifact persistence")
	_, _, err := e.RunOnce(context.Background())
	if err == nil || store.finished != pb.JobState_JOB_STATE_RETRY_WAIT || provider.calls != 1 {
		t.Fatalf("first attempt: %v state=%s calls=%d", err, store.finished, provider.calls)
	}
	store.parkErr = nil
	// Recreate the executor to prove reuse comes from persisted artifacts, not executor memory.
	restarted, err := NewSemanticExecutor(store, e.artifacts, e.model, e.config)
	if err != nil {
		t.Fatal(err)
	}
	_, result, err := restarted.RunOnce(context.Background())
	if err != nil || result.State != pb.JobState_JOB_STATE_WAITING_REVIEW || store.parked == nil || provider.calls != 1 || store.commits != 0 {
		t.Fatalf("recovery resampled or committed: result=%v err=%v calls=%d commits=%d", result, err, provider.calls, store.commits)
	}
}

func TestSemanticExecutorRejectsPinsBeforeModel(t *testing.T) {
	for _, test := range []string{"model pin", "policy pin", "auth", "checkpoint producer", "missing corpus policy"} {
		t.Run(test, func(t *testing.T) {
			e, store, _, provider, _ := executorFixture(t)
			switch test {
			case "model pin":
				store.request.ConfigManifest.InputHashes[1] = parseHash("f")
			case "policy pin":
				store.request.ConfigManifest.InputHashes[0] = parseHash("f")
			case "auth":
				e.config.AuthScope = "scope:other"
			case "checkpoint producer":
				store.checkpoint.Manifest = parseManifest()
			case "missing corpus policy":
				delete(e.config.Policies, store.job.CorpusID)
			}
			_, _, err := e.RunOnce(context.Background())
			if !errors.Is(err, domain.ErrPersistentIntegrity) || store.finished != pb.JobState_JOB_STATE_FAILED || provider.calls != 0 || store.parked != nil {
				t.Fatalf("invalid pin proceeded: err=%v state=%s calls=%d", err, store.finished, provider.calls)
			}
		})
	}
}

type blockingResolver struct {
	started chan struct{}
	err     error
}

func (m blockingResolver) ResolveBatch(ctx context.Context, _ *pb.SemanticResolveRequest) (*pb.SemanticResolveResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	close(m.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestSemanticExecutorCancellationAndRetry(t *testing.T) {
	t.Run("durable cancellation interrupts inference", func(t *testing.T) {
		e, store, _, _, _ := executorFixture(t)
		started := make(chan struct{})
		e.model = blockingResolver{started: started}
		result := make(chan error, 1)
		go func() { _, _, err := e.RunOnce(context.Background()); result <- err }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("inference did not start")
		}
		store.cancelled.Store(true)
		select {
		case err := <-result:
			if !errors.Is(err, errJobCancellationRequested) || store.finished != pb.JobState_JOB_STATE_CANCELLED || store.parked != nil {
				t.Fatalf("cancellation: %v %s", err, store.finished)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancellation did not stop inference")
		}
	})
	t.Run("transient provider failure retries", func(t *testing.T) {
		e, store, _, _, _ := executorFixture(t)
		e.model = blockingResolver{err: status.Error(codes.Unavailable, "provider unavailable")}
		store.job.StageAttempt = 4
		_, _, err := e.RunOnce(context.Background())
		if err == nil || store.finished != pb.JobState_JOB_STATE_RETRY_WAIT || store.retry != 16*time.Second {
			t.Fatalf("retry: %v %s %s", err, store.finished, store.retry)
		}
	})
}

func TestSemanticExecutorRecoveryChecksSourceAuthorization(t *testing.T) {
	for _, drift := range []string{"auth", "config", "ontology"} {
		t.Run(drift, func(t *testing.T) {
			e, store, _, _, _ := executorFixture(t)
			sourceRef := store.refs[store.checkpoint.CompletedBatchKeys[0]]
			store.intent = &domain.SemanticResolutionIntent{Preview: &pb.ResolutionBatch{SourceExtractionBatch: sourceRef,
				Dependencies: &pb.DependencyManifest{ProducerManifest: e.config.Producer}}}
			switch drift {
			case "auth":
				e.config.AuthScope = "scope:other"
			case "config":
				store.request.ConfigManifest.ConfigHash = parseHash("f")
			case "ontology":
				// Keep submitted hash valid for the original ontology, but change the source's version.
				source := new(pb.ExtractionBatch)
				if err := proto.Unmarshal(e.artifacts.(*modelProposalArtifacts).contents[sourceRef.ArtifactId], source); err != nil {
					t.Fatal(err)
				}
				source.OntologyVersion = "foreign-v1"
				raw, _ := proto.Marshal(source)
				ref := semanticRefForTest("artifact:foreign-ontology", raw)
				ref.StorageKey = "objects/foreign-ontology.pb"
				store.refs[ref.ArtifactId] = ref
				e.artifacts.(*modelProposalArtifacts).contents[ref.ArtifactId] = raw
				store.intent.Preview.SourceExtractionBatch = ref
			}
			_, _, err := e.RunOnce(context.Background())
			if !errors.Is(err, domain.ErrPersistentIntegrity) || store.commits != 0 || store.finished != pb.JobState_JOB_STATE_FAILED {
				t.Fatalf("recovery bypassed gate: %v commits=%d", err, store.commits)
			}
		})
	}
}

func TestSemanticExecutorEmptyOutputRecovery(t *testing.T) {
	e, store, artifacts, provider, _ := executorFixture(t)
	oldRef := store.refs[store.checkpoint.CompletedBatchKeys[0]]
	source := new(pb.ExtractionBatch)
	if err := proto.Unmarshal(artifacts.contents[oldRef.ArtifactId], source); err != nil {
		t.Fatal(err)
	}
	source.Mentions = nil
	source.Assertions = nil
	source.Supports = nil
	raw, err := proto.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := semanticRefForTest("artifact:empty-executor", raw)
	ref.StorageKey = "objects/empty-executor.pb"
	store.refs[ref.ArtifactId] = ref
	artifacts.contents[ref.ArtifactId] = raw
	store.checkpoint.CompletedBatchKeys = []string{ref.ArtifactId}
	store.checkpoint.ArtifactHashes = []*pb.ContentHash{ref.ContentHash}
	_, result, err := e.RunOnce(context.Background())
	if err != nil || result.State != pb.JobState_JOB_STATE_STAGED || provider.calls != 0 {
		t.Fatalf("empty execute: %v %v", result, err)
	}
	_, recovered, err := e.RunOnce(context.Background())
	if err != nil || !proto.Equal(result.Artifact, recovered.Artifact) || provider.calls != 0 {
		t.Fatalf("empty recovery: %v %v", recovered, err)
	}
	originalScope := e.config.AuthScope
	e.config.AuthScope = "scope:other"
	if _, _, err = e.RunOnce(context.Background()); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatal("empty checkpoint recovery bypassed scope", err)
	}
	e.config.AuthScope = originalScope
	// A terminal output with no intent must not use the general recovery helper to
	// advance a source that actually contains mentions.
	batch := new(pb.ResolutionBatch)
	if err = proto.Unmarshal(artifacts.contents[result.Artifact.ArtifactId], batch); err != nil {
		t.Fatal(err)
	}
	batch.SourceExtractionBatch = oldRef
	raw, err = proto.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	forged := semanticRefForTest("artifact:nonempty-recovery", raw)
	forged.StorageKey = "objects/nonempty-recovery.pb"
	store.refs[forged.ArtifactId] = forged
	artifacts.contents[forged.ArtifactId] = raw
	store.checkpoint.CompletedBatchKeys = []string{forged.ArtifactId}
	store.checkpoint.ArtifactHashes = []*pb.ContentHash{forged.ContentHash}
	if _, _, err = e.RunOnce(context.Background()); !errors.Is(err, domain.ErrPersistentIntegrity) {
		t.Fatal("nonempty source recovered without intent", err)
	}
}

func TestSemanticExecutorRejectsAggregateLookupBudget(t *testing.T) {
	e, _, _, _, _ := executorFixture(t)
	config := e.config
	config.MaximumAliasesPerScope = domain.MaximumRegistryLookupAliases
	if _, err := NewSemanticExecutor(e.store, e.artifacts, e.model, config); err == nil {
		t.Fatal("policy admitted an impossible aggregate lookup")
	}
}
