// Exercises model resolution against contextual fixtures: authoritative IDs/provenance,
// ambiguous DEFER, invalid output, bounded admission, cancellation, and operation replay.
// Deterministic providers prove integration only; required quality gates need human gold.
package inference

import (
	"context"
	"encoding/json"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"net"
	"os"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"strings"
	"sync"
	"testing"
	"time"
)

func resolutionFixture(provider StructuredProvider) (*SemanticService, *pb.SemanticResolveRequest) {
	extract, original := semanticFixture(provider)
	config := extract.config
	prompt, err := os.ReadFile("../../../../../configs/prompts/resolution-v1.md")
	if err != nil {
		panic(err)
	}
	schema, err := os.ReadFile("../../../../contracts/jsonschema/resolution-output-v1.json")
	if err != nil {
		panic(err)
	}
	config.SystemPrompt, config.OutputSchema = string(prompt), schema
	config.Model = proto.Clone(config.Model).(*pb.ModelManifest)
	config.Model.Task = pb.ModelTask_MODEL_TASK_RESOLVE
	config.Model.PromptHash = &pb.ContentHash{Sha256: sha256String(prompt)}
	config.OutputSchemaHash = &pb.ContentHash{Sha256: sha256String(schema)}
	config.SchemaName = "regulagraph_resolution_v1"
	config.MaximumInputBytes = 32 << 10
	service, err := NewSemanticService(provider, config)
	if err != nil {
		panic(err)
	}
	batch := proto.Clone(original.Batch).(*pb.SemanticBatchContext)
	batch.Model = proto.Clone(config.Model).(*pb.ModelManifest)
	batch.OutputSchema.ContentHash = proto.Clone(config.OutputSchemaHash).(*pb.ContentHash)
	batch.OutputSchema.ByteSize = uint64(len(schema))
	context := original.Items[0]
	mention := &pb.Mention{Meta: extractionMeta("corpus:1", "mention:1"),
		SurfaceForm: "Badan", CandidateType: "organization",
		TextSpan:           &pb.TextSpan{TextArtifactId: "text:1", StartByte: 100, EndByte: 105},
		SourceRefs:         cloneSourceRefs(context.Provenance.Sources),
		ExtractionManifest: proto.Clone(extract.producer).(*pb.ProducerManifest)}
	item := &pb.AmbiguousMention{ItemId: "item:1", Mention: mention,
		Evidence:                 &pb.Provenance{Sources: cloneSourceRefs(mention.SourceRefs), Spans: []*pb.TextSpan{proto.Clone(mention.TextSpan).(*pb.TextSpan)}},
		ExpectedRegistryRevision: 3, ContextItems: []*pb.TextItem{context},
		Candidates: []*pb.CanonicalEntity{{Meta: extractionMeta("corpus:1", "canonical:1"), EntityType: "organization",
			PreferredLabel: "Badan Perizinan", Scope: "ID:national", RegistryRevision: 2, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}},
	}
	return service, &pb.SemanticResolveRequest{Batch: batch, Items: []*pb.AmbiguousMention{item}}
}

const validResolutionJSON = `{"action":"LINK","candidate_id":"canonical:1","rationale":"Identitas sesuai konteks dokumen.","evidence_item_ids":["chunk:1"]}`

func TestResolutionGRPCClientPreservesContextAndRationale(t *testing.T) {
	provider := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
	service, request := resolutionFixture(provider)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterSemanticServer(server, service)
	defer server.Stop()
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///resolution-fixture",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client, err := NewSemanticResolutionClient(connection)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := client.ResolveBatch(ctx, request)
	if err != nil || response.Results[0].GetProposal().GetRationale() == "" ||
		response.Results[0].GetProposal().SupportingContextIds[0] != "chunk:1" {
		t.Fatalf("contextual wire roundtrip failed: %v %v", response, err)
	}
}

func TestResolveProjectsEvidenceAndReusesOperation(t *testing.T) {
	provider := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
	service, request := resolutionFixture(provider)
	response, err := service.ResolveBatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	proposal := response.Results[0].GetProposal()
	if proposal == nil || proposal.Action != pb.ResolutionAction_RESOLUTION_ACTION_LINK ||
		proposal.ExpectedRegistryRevision != 3 || proposal.GetRationale() == "" ||
		!proto.Equal(proposal.Evidence, request.Items[0].Evidence) ||
		len(proposal.SupportingContextIds) != 1 || proposal.Confidence != nil {
		t.Fatalf("lost authoritative resolution binding: %v", proposal)
	}
	if response.Usage.InputTokens != 20 || len(response.Durations) != 3 {
		t.Fatal("missing usage/timing")
	}
	response.Results[0].GetProposal().CandidateIds[0] = "tampered"
	request.Batch.Context.RequestId = "request:retry"
	retry, err := service.ResolveBatch(context.Background(), request)
	if err != nil || provider.callCount() != 1 || retry.RequestId != "request:retry" ||
		retry.Results[0].GetProposal().CandidateIds[0] != "canonical:1" {
		t.Fatalf("unsafe replay: %v %v", retry, err)
	}
	request.Items[0].ContextItems[0].Text += "!"
	request.Items[0].ContextItems[0].Provenance.Spans[0].EndByte++
	if _, err = service.ResolveBatch(context.Background(), request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("context collision accepted: %v", err)
	}
}

func TestResolveDeferPreservesCandidates(t *testing.T) {
	provider := &providerDouble{raw: json.RawMessage(`{"action":"DEFER","candidate_id":"","rationale":"Tidak cukup bukti.","evidence_item_ids":["chunk:1"]}`)}
	service, request := resolutionFixture(provider)
	result, err := service.ResolveBatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	proposal := result.Results[0].GetProposal()
	if proposal == nil || proposal.Action != pb.ResolutionAction_RESOLUTION_ACTION_DEFER || len(proposal.CandidateIds) != 1 {
		t.Fatalf("defer lost candidate: %v", result)
	}
	request.Batch.OperationKey = "operation:empty"
	request.Items[0].Candidates = nil
	result, err = service.ResolveBatch(context.Background(), request)
	if err != nil || result.Results[0].GetProposal() == nil || len(result.Results[0].GetProposal().CandidateIds) != 0 {
		t.Fatalf("empty candidate defer failed: %v %v", result, err)
	}
}

func TestResolveRejectsInvalidContextBeforeProvider(t *testing.T) {
	cases := map[string]func(*pb.SemanticResolveRequest){
		"missing context": func(r *pb.SemanticResolveRequest) { r.Items[0].ContextItems = nil },
		"foreign corpus":  func(r *pb.SemanticResolveRequest) { r.Items[0].Candidates[0].Meta.CorpusId = "foreign" },
		"future revision": func(r *pb.SemanticResolveRequest) { r.Items[0].Candidates[0].RegistryRevision = 4 },
		"wrong source": func(r *pb.SemanticResolveRequest) {
			r.Items[0].ContextItems[0].Provenance.Sources[0].SourceBlobId = "foreign"
		},
		"wrong surface":  func(r *pb.SemanticResolveRequest) { r.Items[0].Mention.SurfaceForm = "Badan lain" },
		"wrong evidence": func(r *pb.SemanticResolveRequest) { r.Items[0].Evidence.Spans[0].StartByte++ },
		"duplicate item": func(r *pb.SemanticResolveRequest) {
			r.Items = append(r.Items, proto.Clone(r.Items[0]).(*pb.AmbiguousMention))
		},
		"wrong type":   func(r *pb.SemanticResolveRequest) { r.Items[0].Candidates[0].EntityType = "regulation" },
		"schema bytes": func(r *pb.SemanticResolveRequest) { r.Batch.OutputSchema.ByteSize++ },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			provider := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
			service, request := resolutionFixture(provider)
			mutate(request)
			if _, err := service.ResolveBatch(context.Background(), request); err == nil || provider.callCount() != 0 {
				t.Fatalf("invalid request reached provider: %v", err)
			}
		})
	}
}

func TestResolveRejectsInventedOutputPerItem(t *testing.T) {
	cases := []string{
		strings.Replace(validResolutionJSON, "canonical:1", "canonical:invented", 1),
		strings.Replace(validResolutionJSON, "chunk:1", "chunk:invented", 1),
		strings.Replace(validResolutionJSON, `"LINK"`, `"MERGE"`, 1),
		strings.Replace(validResolutionJSON, `"LINK"`, `"DEFER"`, 1),
		validResolutionJSON + "{}",
		strings.Replace(validResolutionJSON, `"action"`, `"untrusted_action"`, 1),
		`{"action":"DEFER","rationale":"uncertain","evidence_item_ids":["chunk:1"]}`,
		`{"action":"DEFER","candidate_id":null,"rationale":"uncertain","evidence_item_ids":["chunk:1"]}`,
		`{"action":"DEFER","action":"LINK","candidate_id":"canonical:1","rationale":"ambiguous JSON","evidence_item_ids":["chunk:1"]}`,
	}
	for _, raw := range cases {
		provider := &providerDouble{raw: json.RawMessage(raw)}
		service, request := resolutionFixture(provider)
		response, err := service.ResolveBatch(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		failure := response.Results[0].GetError()
		if failure == nil || failure.Retryable || failure.Stage != "semantic.resolve" {
			t.Fatalf("invalid output accepted: %s -> %v", raw, response)
		}
	}
}

func TestResolveContextCanCarryBroaderVersionWithoutRelabelingMention(t *testing.T) {
	provider := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
	service, request := resolutionFixture(provider)
	request.Items[0].ContextItems[0].Provenance.Sources[0].ProvisionVersionId = "version:parent"
	result, err := service.ResolveBatch(context.Background(), request)
	if err != nil || result.Results[0].GetProposal().Evidence.Sources[0].ProvisionVersionId != "version:1" {
		t.Fatalf("context changed exact mention provenance: %v %v", result, err)
	}
}

func TestResolveCancelledProviderDoesNotPoisonRetry(t *testing.T) {
	provider := &resolveBlockingProvider{entered: make(chan struct{}, 1)}
	service, request := resolutionFixture(provider)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := service.ResolveBatch(ctx, request); finished <- err }()
	<-provider.entered
	cancel()
	if err := <-finished; status.Code(err) != codes.Canceled {
		t.Fatal(err)
	}
	replacement := &providerDouble{raw: json.RawMessage(validResolutionJSON)}
	service.provider = replacement
	result, err := service.ResolveBatch(context.Background(), request)
	if err != nil || result.Results[0].GetProposal() == nil || replacement.callCount() != 1 {
		t.Fatalf("cancelled provider outcome was cached as terminal: %v %v", result, err)
	}
}

func TestResolutionCacheEvictsAndCoalesces(t *testing.T) {
	cache := newResolutionCache(1, 1024)
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	var mu sync.Mutex
	run := func(map[string]resolveOutcome) (*pb.SemanticResolveResponse, map[string]resolveOutcome, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		close(entered)
		<-release
		return &pb.SemanticResolveResponse{RequestId: "request:one"}, nil, nil
	}
	done := make(chan error, 2)
	go func() { _, err := cache.execute(context.Background(), "one", [32]byte{1}, run); done <- err }()
	<-entered
	go func() { _, err := cache.execute(context.Background(), "one", [32]byte{1}, run); done <- err }()
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("concurrent replay called provider %d times", calls)
	}
	simple := func(map[string]resolveOutcome) (*pb.SemanticResolveResponse, map[string]resolveOutcome, error) {
		return &pb.SemanticResolveResponse{RequestId: "request:two"}, nil, nil
	}
	if _, err := cache.execute(context.Background(), "two", [32]byte{2}, simple); err != nil {
		t.Fatal(err)
	}
	if len(cache.entries) != 1 || cache.entries["one"] != nil {
		t.Fatal("entry count limit not enforced")
	}
	cache.maximumBytes = 1
	if _, err := cache.execute(context.Background(), "three", [32]byte{3}, simple); err != nil {
		t.Fatal(err)
	}
	if cache.bytes > 1 || len(cache.entries) != 0 {
		t.Fatal("byte limit not enforced")
	}
}

type resolutionRetryProvider struct {
	mu    sync.Mutex
	calls map[string]int
}

func (p *resolutionRetryProvider) Generate(_ context.Context, r StructuredRequest) (StructuredResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[r.ItemID]++
	if r.ItemID == "item:2" && p.calls[r.ItemID] == 1 {
		return StructuredResponse{}, &ProviderError{Retryable: true, Safe: "transient"}
	}
	return StructuredResponse{JSON: json.RawMessage(validResolutionJSON), InputTokens: 10, OutputTokens: 5}, nil
}
func TestResolvePartialRetryReusesTerminalItems(t *testing.T) {
	provider := &resolutionRetryProvider{calls: map[string]int{}}
	service, request := resolutionFixture(provider)
	second := proto.Clone(request.Items[0]).(*pb.AmbiguousMention)
	second.ItemId = "item:2"
	second.Mention.Meta.RecordId = "mention:2"
	request.Items = append(request.Items, second)
	first, err := service.ResolveBatch(context.Background(), request)
	if err != nil || first.Results[1].GetError() == nil {
		t.Fatalf("missing retryable failure: %v %v", first, err)
	}
	secondResponse, err := service.ResolveBatch(context.Background(), request)
	if err != nil || secondResponse.Results[1].GetProposal() == nil || provider.calls["item:1"] != 1 || provider.calls["item:2"] != 2 {
		t.Fatalf("partial replay failed: %v %v %v", secondResponse, err, provider.calls)
	}
}

type resolveBlockingProvider struct{ entered chan struct{} }

func (p *resolveBlockingProvider) Generate(ctx context.Context, _ StructuredRequest) (StructuredResponse, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return StructuredResponse{}, ctx.Err()
}
func TestResolveCancellationAndAdmission(t *testing.T) {
	provider := &resolveBlockingProvider{entered: make(chan struct{}, 1)}
	service, request := resolutionFixture(provider)
	service.operations = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := service.ResolveBatch(ctx, request); finished <- err }()
	select {
	case <-provider.entered:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	if _, err := service.ResolveBatch(context.Background(), request); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("admission was unbounded: %v", err)
	}
	cancel()
	select {
	case err := <-finished:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("cancel lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation leaked work")
	}
}
