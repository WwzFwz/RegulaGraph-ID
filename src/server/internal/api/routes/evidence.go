// Evidence is the bounded HTTP boundary around the pinned production workflow.
// A local operator token authorizes one corpus/profile; callers cannot supply
// trusted RequestContext or backend routing. C01 QuestionRequest/EvidenceBundle
// remain the payload schemas. Overload, cancellation and dependency failure stay
// errors, never empty successful answers. Measure queue-inclusive p95/p99 and
// rejection rate under configs/benchmark-targets.yaml; limits are not benchmarks.
package routes

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type EvidenceService interface {
	Search(context.Context, *pb.QuestionRequest) (*pb.EvidenceBundle, error)
	Ready(context.Context) error
}

type EvidenceConfig struct {
	Token, Corpus string
	EnableAnswers bool
	Profile       pb.RetrievalProfile
	Concurrent    int
	Timeout       time.Duration
}

type Evidence struct {
	service EvidenceService
	answers AnswerService
	config  EvidenceConfig
	token   [32]byte
	slots   chan struct{}
}

func NewEvidence(service EvidenceService, cfg EvidenceConfig) (*Evidence, error) {
	if service == nil {
		return nil, errors.New("evidence service required")
	}
	if err := ValidateEvidenceConfig(cfg); err != nil {
		return nil, err
	}
	token := sha256.Sum256([]byte(cfg.Token))
	cfg.Token = ""
	var answers AnswerService
	if cfg.EnableAnswers {
		var ok bool
		answers, ok = service.(AnswerService)
		if !ok || !answers.AnswerEnabled() {
			return nil, errors.New("answer service required when enabled")
		}
	}
	return &Evidence{service: service, answers: answers, config: cfg, token: token, slots: make(chan struct{}, cfg.Concurrent)}, nil
}

func ValidateEvidenceConfig(cfg EvidenceConfig) error {
	if len(cfg.Token) < 32 || len(cfg.Token) > 256 || strings.ContainsAny(cfg.Token, " \t\r\n") || cfg.Corpus == "" || cfg.Concurrent < 1 || cfg.Concurrent > 128 || cfg.Timeout < time.Second || cfg.Timeout > 5*time.Minute || (cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG && cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG && cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG && cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG) {
		return errors.New("invalid evidence HTTP configuration")
	}
	return nil
}

func evidenceError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"`+code+`"}`+"\n")
}

func (h *Evidence) authorize(w http.ResponseWriter, r *http.Request) bool {
	// No browser origin is accepted by this operator API. A future browser UI
	// must introduce explicit origin/auth policy instead of implicit CORS access.
	if len(r.Header.Values("Origin")) != 0 {
		evidenceError(w, 403, "origin_not_allowed")
		return false
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		evidenceError(w, 401, "unauthorized")
		return false
	}
	hash := sha256.Sum256([]byte(strings.TrimPrefix(values[0], "Bearer ")))
	if subtle.ConstantTimeCompare(hash[:], h.token[:]) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		evidenceError(w, 401, "unauthorized")
		return false
	}
	return true
}

func (h *Evidence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorize(w, r) {
		return
	}
	ready := r.URL.Path == "/readyz"
	answer := r.URL.Path == "/v1/questions" && h.answers != nil
	if !ready && !answer && r.URL.Path != "/v1/evidence" {
		evidenceError(w, 404, "not_found")
		return
	}
	method := http.MethodPost
	if ready {
		method = http.MethodGet
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		evidenceError(w, 405, "method_not_allowed")
		return
	}
	if r.URL.RawQuery != "" {
		evidenceError(w, 400, "query_parameters_not_supported")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.config.Timeout)
	defer cancel()
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		evidenceError(w, 429, "overloaded")
		return
	}
	if ready {
		if err := h.service.Ready(ctx); err != nil {
			evidenceError(w, 503, "not_ready")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		capability := "evidence"
		if h.answers != nil {
			capability = "evidence_and_answer_draft"
		}
		_, _ = io.WriteString(w, "{\"status\":\"ready\",\"capability\":\""+capability+"\"}\n")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		evidenceError(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			evidenceError(w, 413, "request_too_large")
		} else {
			evidenceError(w, 400, "invalid_body")
		}
		return
	}
	request := new(pb.QuestionRequest)
	if err = (protojson.UnmarshalOptions{RecursionLimit: 64}).Unmarshal(raw, request); err != nil {
		evidenceError(w, 400, "invalid_question")
		return
	}
	if err = domain.ValidateWire(request, domain.DefaultWireLimits); err != nil || strings.TrimSpace(request.Question) == "" {
		evidenceError(w, 400, "invalid_question")
		return
	}
	if request.CorpusId != h.config.Corpus {
		evidenceError(w, 403, "corpus_not_authorized")
		return
	}
	if request.RequestedProfile != h.config.Profile || request.ResponseMode != pb.ResponseMode_RESPONSE_MODE_COMPLETE || request.TemporalScope == nil || request.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || request.TemporalScope.EffectiveAt == nil || len(request.TemporalScope.CompareDates) != 0 {
		evidenceError(w, 400, "unsupported_query_mode")
		return
	}
	if answer {
		h.serveAnswer(ctx, w, request)
		return
	}
	result, err := h.service.Search(ctx, request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			evidenceError(w, 504, "deadline_exceeded")
		} else if errors.Is(ctx.Err(), context.Canceled) {
			evidenceError(w, 408, "cancelled")
		} else {
			evidenceError(w, 503, "evidence_unavailable")
		}
		return
	}
	if ctx.Err() != nil {
		evidenceError(w, 504, "deadline_exceeded")
		return
	}
	if result == nil || result.Meta == nil || result.Meta.CorpusId != h.config.Corpus || domain.ValidateWire(result, domain.DefaultWireLimits) != nil {
		evidenceError(w, 502, "invalid_workflow_output")
		return
	}
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(result)
	if err != nil || len(encoded) > 16<<20 {
		evidenceError(w, 502, "invalid_workflow_output")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Evidence-ID", result.Meta.RecordId)
	_, _ = w.Write(encoded)
}
