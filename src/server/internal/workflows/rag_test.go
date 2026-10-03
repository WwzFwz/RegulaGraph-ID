// Exercises search/fusion -> hydration accounting -> context -> structured draft
// -> source citation through production workflow components. Backend/model/data
// doubles are explicitly synthetic and do not prove a usable published corpus,
// model quality, semantic support, exact tokenizer parity or required latency.
package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/retrieval"
)

type ragProvider struct{ calls int }

func (p *ragProvider) Generate(context.Context, inference.StructuredRequest) (inference.StructuredResponse, error) {
	p.calls++
	return inference.StructuredResponse{JSON: json.RawMessage(`{"status":"answer","claims":[{"text":"Perizinan diperlukan.","evidence_ids":["index:one"]}]}`), InputTokens: 100, OutputTokens: 20}, nil
}

func ragFixture(t *testing.T) (*RAGWorkflow, *pb.QuestionRequest, retrieval.SearchInput, *ragProvider) {
	t.Helper()
	search, input := candidateWorkflowFixture()
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	snapshot := &pb.SnapshotRef{CorpusId: "corpus:one", SnapshotId: "snapshot:one", Sequence: 7, ManifestHash: hash, RepresentationGeneration: "generation:one"}
	input.Question = "Apakah perizinan diperlukan?"
	input.Context = &pb.RequestContext{SchemaVersion: 1, RequestId: "request:one", TraceId: "trace:one", CorpusId: "corpus:one", AuthScopeRef: "auth:one", ConfigFingerprint: hash, SnapshotRef: snapshot, Deadline: timestamppb.New(time.Now().Add(time.Minute))}
	model := &pb.ModelManifest{ModelId: "generator:test", Version: "1", WeightsHash: hash, TokenizerHash: hash, Task: pb.ModelTask_MODEL_TASK_GENERATE, MaxTokens: 4096, Precision: "fp16", Backend: "test", PromptHash: answering.DraftPromptHash()}
	producer := &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: hash, Models: []*pb.ModelManifest{model}, PromptHashes: []*pb.ContentHash{answering.DraftPromptHash()}}
	provider := &ragProvider{}
	g, err := answering.NewDraftGenerator(provider, func(context.Context, inference.StructuredRequest) (uint64, error) { return 100, nil }, answering.DraftGeneratorConfig{Model: model, Producer: producer, MaximumInputBytes: 1 << 20, MaximumOutputBytes: 64 << 10, MaximumClaims: 8, MaximumCitations: 32, MaximumConcurrent: 1, OutputTokens: 512, AllowUnreviewedDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	hydrate := func(context.Context, *pb.QuestionRequest, *CandidateSearchResult) (*HydratedCandidates, error) {
		return &HydratedCandidates{Evidence: &pb.EvidenceBundle{
			Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "bundle:one"}, Snapshot: proto.Clone(snapshot).(*pb.SnapshotRef), RetrievalManifest: producer, Completeness: pb.Completeness_COMPLETENESS_COMPLETE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
			Items: []*pb.Evidence{{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:one", RecordId: "index:one"}, Text: "Perizinan diperlukan.", SnapshotRef: proto.Clone(snapshot).(*pb.SnapshotRef), LegalStatus: pb.LegalStatus_LEGAL_STATUS_UNKNOWN,
				SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "source:one", RegulationId: "regulation:one", ProvisionVersionId: "version:one"}}, SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:one", EndByte: 20}}}}},
			SourceURLs: func(string, string) ([]string, error) { return []string{"https://example.org/source.pdf"}, nil }}, nil
	}
	w := &RAGWorkflow{Search: search, Hydrate: hydrate, Answer: &EvidenceAnswerWorkflow{Generator: g, ContextTokenizer: hash, MaximumContextTokens: 2048, MaximumEvidence: 10, CountContext: func(_ context.Context, text string) (uint64, error) { return uint64(len(text)), nil }}}
	request := &pb.QuestionRequest{Question: input.Question, CorpusId: "corpus:one", ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE, RequestedProfile: pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG,
		TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}
	return w, request, input, provider
}

func TestRAGWorkflowConnectsSearchToCitedDraft(t *testing.T) {
	w, request, input, provider := ragFixture(t)
	result, err := w.AnswerPinnedQuestion(context.Background(), request, input)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || len(result.Answer.Draft.Answer.Citations) != 1 || result.Answer.Draft.Answer.Claims[0].SupportStatus != pb.SupportStatus_SUPPORT_STATUS_UNREVIEWED {
		t.Fatal("draft output or source mapping incorrect")
	}
	if len(result.Evidence.Items[0].CandidateProvenance) != 1 || result.Evidence.Items[0].CandidateProvenance[0].Retriever != pb.RetrieverKind_RETRIEVER_KIND_DENSE {
		t.Fatal("lost retrieval provenance")
	}
}

func TestRAGWorkflowRejectsHydrationGapsBeforeModel(t *testing.T) {
	for _, mode := range []string{"missing", "foreign version", "snapshot", "unaccounted rejection", "request snapshot", "expired", "mutated search view"} {
		t.Run(mode, func(t *testing.T) {
			w, request, input, provider := ragFixture(t)
			hydrate := w.Hydrate
			w.Hydrate = func(c context.Context, r *pb.QuestionRequest, s *CandidateSearchResult) (*HydratedCandidates, error) {
				h, err := hydrate(c, r, s)
				switch mode {
				case "missing":
					h.Evidence.Items = nil
				case "foreign version":
					h.Evidence.Items[0].SourceRefs[0].ProvisionVersionId = "foreign"
				case "mutated search view":
					s.Hits["index:one"].ProvisionVersionIDs[0] = "foreign"
					h.Evidence.Items[0].SourceRefs[0].ProvisionVersionId = "foreign"
				case "snapshot":
					h.Evidence.Snapshot.Sequence++
				case "unaccounted rejection":
					h.Rejected = map[string]string{"missing": "excluded"}
				}
				return h, err
			}
			if mode == "request snapshot" {
				request.SnapshotId = proto.String("other")
			}
			if mode == "expired" {
				input.Context.Deadline = timestamppb.New(time.Now().Add(-time.Second))
			}
			if _, err := w.AnswerPinnedQuestion(context.Background(), request, input); err == nil {
				t.Fatal("invalid boundary accepted")
			}
			if provider.calls != 0 {
				t.Fatal("invalid evidence reached model")
			}
		})
	}
}
