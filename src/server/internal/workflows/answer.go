// Menyusun persiapan query, jalur retrieval, fusion, reranking, context building, dan generation.
//
// Peran dalam komponen:
// Menjadi orchestration tanya jawab yang sama untuk API, CLI, serta eksperimen.
//
// Kontrak integrasi dan perhatian implementasi:
// Dukung konfigurasi empat metode tanpa menyalin pipeline; graph bisa berawal dari linking atau retrieval; catat latency tiap tahap.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
//
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: workflow draft dari EvidenceBundle terpin aktif; graph profile menuntut
// immutable GraphContext untuk rendering/citation/final validation. Empat profil
// retrieval terhubung di library; automatic seed linking/API graph, tokenizer
// generator nyata dan streaming masih perlu disambungkan.
// Integrasi berikutnya:
// Pin one snapshot, resolve temporal intent, coordinate retrieval/context/generation and validate terminal evidence; propagate cancellation.
// Bukti verifikasi: Test unavailable dependencies, evidence conflicts and snapshot rollover mid-request; trace queue and stage durations.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package workflows

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/domain"
)

// EvidenceAnswerWorkflow consumes already authenticated/hydrated retrieval
// output. Caller owns the read lease until return and supplies a deadline no
// later than lease expiry. No external endpoint may accept a user's bundle as
// trusted input. This stage does not independently acquire a publication pin.
type EvidenceAnswerWorkflow struct {
	Generator            *answering.DraftGenerator
	ContextTokenizer     *pb.ContentHash
	CountContext         answering.TokenCounter
	MaximumContextTokens uint64
	MaximumEvidence      int
}

type EvidenceAnswerResult struct {
	Draft    *answering.DraftResult
	Context  *pb.ContextBundle
	Duration time.Duration
}

// AnswerEvidence connects production context packing, provider generation and
// citation checks. Graph profiles require an admitted GraphContext; all profiles
// require explicit AS_OF non-streaming requests. CURRENT/COMPARE need planning; silently
// substituting today's date would misrepresent a legal-time question.
func (w *EvidenceAnswerWorkflow) AnswerEvidence(ctx context.Context, request *pb.QuestionRequest,
	call *pb.RequestContext, bundle *pb.EvidenceBundle, urls domain.SourceURLLookup, graphPlans ...*answering.GraphContext) (*EvidenceAnswerResult, error) {
	started := time.Now()
	if ctx == nil || w == nil || w.Generator == nil || w.CountContext == nil || request == nil || call == nil || bundle == nil || urls == nil {
		return nil, errors.New("answer workflow dependencies and trusted pinned inputs required")
	}
	for _, message := range []proto.Message{request, call, bundle} {
		if err := domain.ValidateWire(message, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	if request.ResponseMode != pb.ResponseMode_RESPONSE_MODE_COMPLETE || request.TemporalScope == nil ||
		request.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || request.TemporalScope.EffectiveAt == nil ||
		len(request.TemporalScope.CompareDates) != 0 || request.CorpusId != call.CorpusId || request.CorpusId != bundle.Meta.CorpusId ||
		call.SnapshotRef == nil || !proto.Equal(call.SnapshotRef, bundle.Snapshot) ||
		(request.SnapshotId != nil && *request.SnapshotId != bundle.Snapshot.SnapshotId) ||
		(request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, bundle.Snapshot)) {
		return nil, errors.New("answer requires explicit date, consistent authorized snapshot and complete response mode")
	}
	var graphPlan *answering.GraphContext
	if len(graphPlans) > 1 {
		return nil, errors.New("one graph context plan allowed")
	}
	if len(graphPlans) == 1 {
		graphPlan = graphPlans[0]
	}
	switch request.RequestedProfile {
	case pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG:
		if graphPlan != nil {
			return nil, errors.New("graph context requires explicit graph profile")
		}
	case pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG, pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG:
		if graphPlan == nil {
			return nil, errors.New("graph answering requires admitted rendering plan")
		}
	default:
		return nil, errors.New("unsupported answer retrieval profile")
	}
	bounded, cancel := context.WithDeadline(ctx, call.Deadline.AsTime())
	defer cancel()
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	rendered, err := answering.BuildContext(bounded, bundle, "context:"+call.RequestId, w.ContextTokenizer, w.MaximumContextTokens, w.MaximumEvidence, w.CountContext, graphPlan)
	if err != nil {
		return nil, err
	}
	draft, err := w.Generator.Generate(bounded, answering.DraftInput{RequestID: call.RequestId, RecordID: "answer:" + call.RequestId, Question: request.Question,
		EffectiveDates: []*pb.CalendarDate{request.TemporalScope.EffectiveAt}, Context: rendered, Evidence: bundle, SourceURLs: urls, GraphContext: graphPlan})
	if err != nil {
		return nil, err
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	return &EvidenceAnswerResult{Draft: draft, Context: rendered, Duration: time.Since(started)}, nil
}
