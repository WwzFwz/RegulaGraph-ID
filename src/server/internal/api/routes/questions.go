// Menjadi endpoint tanya jawab yang memanggil workflow answer.
//
// Peran dalam komponen:
// Menyajikan hasil dan citation melalui schema HTTP.
//
// Kontrak integrasi dan perhatian implementasi:
// Tidak memanggil database/model secara langsung; deadline dan cancellation perlu diteruskan tanpa menyamarkan jawaban parsial.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
//
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: complete-response draft endpoint aktif melalui shared Evidence handler.
// Auth, corpus/profile, body/deadline dan concurrency memakai gate yang sama;
// generation gagal tidak fallback menjadi evidence-only. Streaming belum aktif.
// Workflow memiliki citation/source checks; route menolak promosi status dan drift.
// Bukti verifikasi: Test client disconnect, exactly one terminal event, partial generation and unavailable snapshot; record TTFT and total latency.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/workflows"
)

// AnswerService accepts an untrusted question, never caller-supplied evidence.
// Its workflow owns the snapshot lease and authenticates citation provenance.
type AnswerService interface {
	Answer(context.Context, *pb.QuestionRequest) (*workflows.RAGResult, error)
	AnswerEnabled() bool
}

func (h *Evidence) serveAnswer(ctx context.Context, w http.ResponseWriter, request *pb.QuestionRequest) {
	result, err := h.answers.Answer(ctx, request)
	if err != nil || ctx.Err() != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
			evidenceError(w, 504, "deadline_exceeded")
		case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
			evidenceError(w, 408, "cancelled")
		default:
			evidenceError(w, 503, "answer_unavailable")
		}
		return
	}
	raw, err := encodeAnswerDraft(request, result)
	if err != nil || len(raw) > 16<<20 {
		evidenceError(w, 502, "invalid_workflow_output")
		return
	}
	if ctx.Err() != nil {
		evidenceError(w, 504, "deadline_exceeded")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err = writeTemporalHeaders(w, request, result, h.config.TimeZone); err != nil {
		evidenceError(w, 502, "invalid_workflow_output")
		return
	}
	w.Header().Set("X-Evidence-ID", result.Evidence.Meta.RecordId)
	w.Header().Set("X-Answer-ID", result.Answer.Draft.Answer.Meta.RecordId)
	_, _ = w.Write(raw)
}

func encodeAnswerDraft(request *pb.QuestionRequest, result *workflows.RAGResult) ([]byte, error) {
	if result == nil || result.Comparison != nil || result.Evidence == nil || result.Answer == nil || result.Answer.Draft == nil || result.Answer.Draft.Answer == nil {
		return nil, errors.New("missing answer draft")
	}
	b, draft := result.Evidence, result.Answer.Draft
	a := draft.Answer
	effectiveDate, err := resultEffectiveDate(request, result)
	if err != nil {
		return nil, err
	}
	for _, message := range []proto.Message{b, a} {
		if err := domain.ValidateWire(message, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	if a.Meta.CorpusId != request.CorpusId || b.Meta.CorpusId != request.CorpusId || !proto.Equal(a.Snapshot, b.Snapshot) || a.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || b.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || (a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_PARTIAL && a.SemanticStatus != pb.SemanticStatus_SEMANTIC_STATUS_ABSTAIN) || len(a.EffectiveDates) != 1 || !proto.Equal(a.EffectiveDates[0], effectiveDate) {
		return nil, errors.New("answer identity, date or draft status mismatch")
	}
	if (request.SnapshotId != nil && *request.SnapshotId != a.Snapshot.SnapshotId) || (request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, a.Snapshot)) {
		return nil, errors.New("answer snapshot differs from request")
	}
	for _, claim := range a.Claims {
		if claim.SupportStatus != pb.SupportStatus_SUPPORT_STATUS_UNREVIEWED {
			return nil, errors.New("model claim promoted to reviewed")
		}
	}
	answerJSON, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(a)
	if err != nil {
		return nil, err
	}
	evidenceJSON, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(b)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Mode         string          `json:"mode"`
		Answer       json.RawMessage `json:"answer"`
		Evidence     json.RawMessage `json:"evidence"`
		InputTokens  uint64          `json:"input_tokens"`
		OutputTokens uint64          `json:"output_tokens"`
	}{"answer_draft", answerJSON, evidenceJSON, draft.InputTokens, draft.OutputTokens})
}
