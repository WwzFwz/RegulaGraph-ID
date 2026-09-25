// Tests durable ingestion scheduling decisions before any external worker or database side effect.
// These fixtures protect ACQUIRE/PARSE ownership and do not measure production throughput; latency and
// ingestion gates remain sourced from configs/benchmark-targets.yaml as REQUIRED_UNMEASURED.
package workflows

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type ontologySubmitStore struct {
	*parseStoreFake
	submitted []domain.JobIntent
}

func (store *ontologySubmitStore) SubmitJob(_ context.Context, intent domain.JobIntent) (domain.JobRecord, bool, error) {
	store.submitted = append(store.submitted, intent)
	return domain.JobRecord{}, false, nil
}

func TestJobSchedulerRejectsUnpinnedOntologyBeforeDurableSubmit(t *testing.T) {
	store := &ontologySubmitStore{parseStoreFake: parseFixtureStore(false)}
	policy := domain.CandidatePlanningPolicy{ScopesByType: map[string][]string{"organization": {"ID:national"}},
		MaximumMentions: 100, MaximumScopesPerMention: 4, MaximumTotalScopes: 400}
	scheduler, err := NewJobScheduler(store, parseTestOntology(),
		map[string]domain.CandidatePlanningPolicy{store.request.CorpusId: policy})
	if err != nil {
		t.Fatal(err)
	}
	policyHash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	request := proto.Clone(store.request).(*pb.IngestionRequest)
	request.ConfigManifest.InputHashes = nil
	if _, _, err := scheduler.Submit(context.Background(), "job:unbound", request); err == nil || len(store.submitted) != 0 {
		t.Fatalf("unpinned request reached durable store: err=%v intents=%d", err, len(store.submitted))
	}
	request.ConfigManifest.InputHashes = []*pb.ContentHash{{Sha256: strings.Repeat("f", 64)}}
	if _, _, err := scheduler.Submit(context.Background(), "job:wrong-pin", request); err == nil || len(store.submitted) != 0 {
		t.Fatalf("wrong ontology hash reached durable store: err=%v intents=%d", err, len(store.submitted))
	}
	request.ConfigManifest.InputHashes = []*pb.ContentHash{parseTestOntology().ContentHash()}
	if _, _, err := scheduler.Submit(context.Background(), "job:missing-policy", request); err == nil || len(store.submitted) != 0 {
		t.Fatalf("missing policy pin reached durable store: err=%v intents=%d", err, len(store.submitted))
	}
	request.ConfigManifest.InputHashes = append(request.ConfigManifest.InputHashes, &pb.ContentHash{Sha256: strings.Repeat("e", 64)})
	if _, _, err := scheduler.Submit(context.Background(), "job:wrong-policy", request); err == nil || len(store.submitted) != 0 {
		t.Fatalf("wrong policy pin reached durable store: err=%v intents=%d", err, len(store.submitted))
	}
	request.ConfigManifest.InputHashes[1] = policyHash
	if _, _, err := scheduler.Submit(context.Background(), "job:pinned", request); err != nil || len(store.submitted) != 1 {
		t.Fatalf("valid ontology/policy pins did not reach durable store: err=%v intents=%d", err, len(store.submitted))
	}
	stored := new(pb.IngestionRequest)
	if err := proto.Unmarshal(store.submitted[0].RequestPayload, stored); err != nil ||
		!containsExpectedHash(stored.GetConfigManifest().GetInputHashes(), policyHash) ||
		store.submitted[0].CorpusID != request.CorpusId {
		t.Fatalf("durable job lost candidate policy pin: err=%v intent=%+v", err, store.submitted[0])
	}
	request.CorpusId = "corpus:foreign"
	if _, _, err := scheduler.Submit(context.Background(), "job:foreign", request); err == nil || len(store.submitted) != 1 {
		t.Fatalf("unconfigured corpus reused another policy: err=%v intents=%d", err, len(store.submitted))
	}
}

func TestJobSchedulerFreezesPolicyAtConstruction(t *testing.T) {
	store := &ontologySubmitStore{parseStoreFake: parseFixtureStore(false)}
	policy := domain.CandidatePlanningPolicy{ScopesByType: map[string][]string{"organization": {"ID:national"}},
		MaximumMentions: 100, MaximumScopesPerMention: 4, MaximumTotalScopes: 400}
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewJobScheduler(store, parseTestOntology(),
		map[string]domain.CandidatePlanningPolicy{store.request.CorpusId: policy})
	if err != nil {
		t.Fatal(err)
	}
	policy.ScopesByType["organization"][0] = "ID:regional"
	request := proto.Clone(store.request).(*pb.IngestionRequest)
	request.ConfigManifest.InputHashes = append(request.ConfigManifest.InputHashes, hash)
	if _, _, err := scheduler.Submit(context.Background(), "job:frozen", request); err != nil || len(store.submitted) != 1 {
		t.Fatalf("mutating caller policy changed scheduler snapshot: %v", err)
	}
	if _, err := NewJobScheduler(store, parseTestOntology(), nil); err == nil {
		t.Fatal("scheduler accepted no corpus policy")
	}
	if _, err := NewJobScheduler(store, parseTestOntology(), map[string]domain.CandidatePlanningPolicy{
		"corpus:bad": {ScopesByType: map[string][]string{"unknown_type": {"ID:national"}}},
	}); err == nil {
		t.Fatal("scheduler accepted invalid candidate policy")
	}
}

func TestInitialIngestionStageRequiresAllSourcesAcquired(t *testing.T) {
	blob := &pb.SourceLocator{PortalId: "bpk", Locator: &pb.SourceLocator_Blob{Blob: &pb.ArtifactRef{
		ArtifactId: "source-1", ContentHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)},
		StorageKey: "inputs/source.pdf", MediaType: "application/pdf", ByteSize: 1, SchemaVersion: 1,
	}}}
	url := &pb.SourceLocator{PortalId: "bpk", Locator: &pb.SourceLocator_Url{Url: "https://example.test/source.pdf"}}
	if got := initialIngestionStage(&pb.IngestionRequest{Sources: []*pb.SourceLocator{blob}}); got != pb.JobStage_JOB_STAGE_PARSE {
		t.Fatalf("acquired sources start at %s", got)
	}
	if got := initialIngestionStage(&pb.IngestionRequest{Sources: []*pb.SourceLocator{blob, url}}); got != pb.JobStage_JOB_STAGE_ACQUIRE {
		t.Fatalf("mixed source request starts at %s", got)
	}
}
