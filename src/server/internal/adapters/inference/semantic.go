// Configures the internal Semantic EXTRACT/RESOLVE gateway and projects strict provider JSON into C01.
// Source text remains untrusted data; the gateway owns IDs, provenance, manifests, review state, and
// absolute UTF-8 byte spans. In-memory operation caching prevents duplicate sampling within a process;
// durable replay across restarts remains a coordinator storage task. Required quality/latency is unmeasured.
package inference

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type SemanticConfig struct {
	Model               *pb.ModelManifest
	Ontology            *domain.Ontology
	OutputSchemaHash    *pb.ContentHash
	SystemPrompt        string
	OutputSchema        json.RawMessage
	SchemaName          string
	Software            string
	Build               string
	ConfigHash          *pb.ContentHash
	TokenizerID         string
	MaximumItems        int
	MaximumInputBytes   int
	MaximumConcurrent   int
	MaximumCacheEntries int
	MaximumCacheBytes   int64
}

type SemanticService struct {
	pb.UnimplementedSemanticServer
	provider        StructuredProvider
	config          SemanticConfig
	producer        *pb.ProducerManifest
	semaphore       chan struct{}
	operations      chan struct{}
	cache           *semanticCache
	resolutionCache *resolutionCache
}

func NewSemanticService(provider StructuredProvider, config SemanticConfig) (*SemanticService, error) {
	if provider == nil {
		return nil, errors.New("structured provider is required")
	}
	if config.Model == nil || config.OutputSchemaHash == nil || config.ConfigHash == nil ||
		config.Ontology == nil || config.SystemPrompt == "" || config.SchemaName == "" ||
		config.Software == "" || config.Build == "" || config.TokenizerID == "" ||
		config.MaximumItems <= 0 || config.MaximumInputBytes <= 0 || config.MaximumConcurrent <= 0 ||
		config.MaximumCacheEntries <= 0 || config.MaximumCacheBytes <= 0 {
		return nil, errors.New("semantic configuration is incomplete")
	}
	if err := domain.ValidateWire(config.Model, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("validate semantic model: %w", err)
	}
	if (config.Model.Task != pb.ModelTask_MODEL_TASK_EXTRACT && config.Model.Task != pb.ModelTask_MODEL_TASK_RESOLVE) || config.Model.PromptHash == nil {
		return nil, errors.New("semantic gateway requires an EXTRACT or RESOLVE model with prompt hash")
	}
	if config.Model.PromptHash.Sha256 != sha256String([]byte(config.SystemPrompt)) {
		return nil, errors.New("semantic system prompt differs from pinned prompt hash")
	}
	if !json.Valid(config.OutputSchema) || config.OutputSchemaHash.Sha256 != sha256String(config.OutputSchema) {
		return nil, errors.New("semantic output schema is invalid or differs from its pinned hash")
	}
	producer := &pb.ProducerManifest{
		Software: config.Software, Build: config.Build, SchemaVersion: 1,
		Models:       []*pb.ModelManifest{proto.Clone(config.Model).(*pb.ModelManifest)},
		PromptHashes: []*pb.ContentHash{proto.Clone(config.Model.PromptHash).(*pb.ContentHash)},
		ConfigHash:   proto.Clone(config.ConfigHash).(*pb.ContentHash),
		InputHashes:  []*pb.ContentHash{proto.Clone(config.OutputSchemaHash).(*pb.ContentHash), config.Ontology.ContentHash()},
	}
	if err := domain.ValidateWire(producer, domain.DefaultWireLimits); err != nil {
		return nil, fmt.Errorf("validate semantic producer: %w", err)
	}
	return &SemanticService{
		provider:        provider,
		config:          config,
		producer:        producer,
		semaphore:       make(chan struct{}, config.MaximumConcurrent),
		operations:      make(chan struct{}, config.MaximumConcurrent),
		cache:           newSemanticCache(config.MaximumCacheEntries, config.MaximumCacheBytes),
		resolutionCache: newResolutionCache(config.MaximumCacheEntries, config.MaximumCacheBytes),
	}, nil
}

// ProducerManifest exports a detached, non-secret configuration identity for operator pinning.
// It performs no provider call; mutating the returned manifest cannot change a live gateway.
func (s *SemanticService) ProducerManifest() *pb.ProducerManifest {
	return proto.Clone(s.producer).(*pb.ProducerManifest)
}

func (s *SemanticService) ExtractBatch(ctx context.Context, request *pb.ExtractBatchRequest) (*pb.ExtractBatchResponse, error) {
	if s.config.Model.Task != pb.ModelTask_MODEL_TASK_EXTRACT {
		return nil, status.Error(codes.FailedPrecondition, "gateway is not configured for EXTRACT")
	}
	if err := s.validateExtractRequest(request); err != nil {
		return nil, err
	}
	executionContext, cancel := context.WithDeadline(ctx, request.Batch.Context.Deadline.AsTime())
	defer cancel()
	if err := executionContext.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	select {
	case s.operations <- struct{}{}:
		defer func() { <-s.operations }()
	default:
		return nil, status.Error(codes.ResourceExhausted, "semantic operation capacity is full")
	}
	fingerprint, err := semanticRequestFingerprint(request)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fingerprint semantic request: %v", err)
	}
	response, err := s.cache.execute(executionContext, request.Batch.OperationKey, fingerprint, func() (*pb.ExtractBatchResponse, error) {
		return s.executeExtract(executionContext, request, fingerprint)
	})
	if err != nil {
		return nil, err
	}
	response.RequestId = request.Batch.Context.RequestId
	return response, nil
}

func (s *SemanticService) validateExtractRequest(request *pb.ExtractBatchRequest) error {
	if err := domain.ValidateWire(request, domain.WireLimits{MaxBytes: s.config.MaximumInputBytes * 2, MaxDepth: 64, MaxItems: s.config.MaximumItems * 16}); err != nil {
		return status.Errorf(codes.InvalidArgument, "validate ExtractBatch: %v", err)
	}
	if request.Batch == nil || request.Batch.Context == nil || request.Batch.Model == nil || request.Batch.OutputSchema == nil {
		return status.Error(codes.InvalidArgument, "semantic batch context is incomplete")
	}
	if err := request.Batch.Context.Deadline.CheckValid(); err != nil {
		return status.Error(codes.InvalidArgument, "semantic request deadline is invalid")
	}
	if !proto.Equal(request.Batch.Model, s.config.Model) || request.Batch.OntologyVersion != s.config.Ontology.Version() {
		return status.Error(codes.FailedPrecondition, "semantic model or ontology differs from the pinned runtime")
	}
	if request.Batch.OutputSchema.MediaType != "application/schema+json" || request.Batch.OutputSchema.SchemaVersion != 1 ||
		!proto.Equal(request.Batch.OutputSchema.ContentHash, s.config.OutputSchemaHash) {
		return status.Error(codes.FailedPrecondition, "semantic output schema differs from the pinned runtime")
	}
	if len(request.Items) > s.config.MaximumItems {
		return status.Error(codes.ResourceExhausted, "semantic item count exceeds the configured limit")
	}
	total := 0
	seen := make(map[string]struct{}, len(request.Items))
	for _, item := range request.Items {
		if item == nil || item.ItemId == "" || item.Text == "" {
			return status.Error(codes.InvalidArgument, "semantic items require an ID and non-empty text")
		}
		if _, duplicate := seen[item.ItemId]; duplicate {
			return status.Error(codes.InvalidArgument, "semantic item IDs must be unique")
		}
		seen[item.ItemId] = struct{}{}
		if len(item.Text) > s.config.MaximumInputBytes-total {
			return status.Error(codes.ResourceExhausted, "semantic input bytes exceed the configured limit")
		}
		total += len(item.Text)
		if item.Provenance == nil || len(item.Provenance.Spans) != 1 || len(item.Provenance.Sources) == 0 {
			return status.Error(codes.InvalidArgument, "semantic item requires one source span and source-version provenance")
		}
		span := item.Provenance.Spans[0]
		if span == nil || span.EndByte < span.StartByte || span.EndByte-span.StartByte != uint64(len(item.Text)) {
			return status.Error(codes.InvalidArgument, "semantic item source span must cover exactly its UTF-8 text bytes")
		}
	}
	return nil
}

type itemOutcome struct {
	result       *pb.ExtractItemResult
	inputTokens  uint64
	outputTokens uint64
}

func (s *SemanticService) executeExtract(ctx context.Context, request *pb.ExtractBatchRequest, fingerprint [32]byte) (*pb.ExtractBatchResponse, error) {
	started := time.Now()
	outcomes := make([]itemOutcome, len(request.Items))
	var wait sync.WaitGroup
	for index, item := range request.Items {
		index, item := index, item
		if cached, ok := s.cache.loadItem(request.Batch.OperationKey, item.ItemId, fingerprint); ok {
			outcomes[index] = cached
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case s.semaphore <- struct{}{}:
				defer func() { <-s.semaphore }()
			case <-ctx.Done():
				outcomes[index].result = itemError(item.ItemId, contextError(ctx.Err()))
				return
			}
			generated, generateErr := s.provider.Generate(ctx, StructuredRequest{
				ModelID: s.config.Model.ModelId, SystemPrompt: s.config.SystemPrompt,
				ItemID: item.ItemId, Text: item.Text, SchemaName: s.config.SchemaName, Schema: s.config.OutputSchema,
			})
			outcomes[index].inputTokens = generated.InputTokens
			outcomes[index].outputTokens = generated.OutputTokens
			if generateErr != nil {
				outcomes[index].result = itemError(item.ItemId, operationError(generateErr, item.ItemId))
				s.cache.storeItem(request.Batch.OperationKey, item.ItemId, fingerprint, outcomes[index])
				return
			}
			proposal, proposalErr := projectExtractionProposal(request, item, generated.JSON, s.producer, s.config.Ontology.Version())
			if proposalErr == nil {
				proposalErr = s.config.Ontology.ValidateExtractionRecords(s.config.Ontology.Version(), proposal.Mentions, proposal.Assertions)
			}
			if proposalErr != nil {
				outcomes[index].result = itemError(item.ItemId, &pb.OperationError{
					Code: pb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, SafeMessage: "provider output failed semantic projection",
					Stage: "semantic.extract", Retryable: false, ItemId: proto.String(item.ItemId),
					Details: []*pb.ErrorDetail{{FieldPath: "structured_output", Reason: proposalErr.Error()}},
				})
				s.cache.storeItem(request.Batch.OperationKey, item.ItemId, fingerprint, outcomes[index])
				return
			}
			if proposalErr = domain.ValidateWire(proposal, domain.WireLimits{MaxBytes: s.config.MaximumInputBytes * 4, MaxDepth: 64, MaxItems: s.config.MaximumItems * 64}); proposalErr != nil {
				outcomes[index].result = itemError(item.ItemId, &pb.OperationError{
					Code: pb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, SafeMessage: "provider proposal violated the graph contract",
					Stage: "semantic.extract", Retryable: false, ItemId: proto.String(item.ItemId),
					Details: []*pb.ErrorDetail{{FieldPath: "structured_output", Reason: proposalErr.Error()}},
				})
				s.cache.storeItem(request.Batch.OperationKey, item.ItemId, fingerprint, outcomes[index])
				return
			}
			outcomes[index].result = &pb.ExtractItemResult{ItemId: item.ItemId, Result: &pb.ExtractItemResult_Proposal{Proposal: proposal}}
			s.cache.storeItem(request.Batch.OperationKey, item.ItemId, fingerprint, outcomes[index])
		}()
	}
	wait.Wait()
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	response := &pb.ExtractBatchResponse{
		RequestId:        request.Batch.Context.RequestId,
		Model:            proto.Clone(s.config.Model).(*pb.ModelManifest),
		Usage:            &pb.TokenUsage{TokenizerId: s.config.TokenizerID},
		Durations:        []*pb.StageDuration{{Stage: "semantic_extract_total", DurationNs: uint64(time.Since(started))}},
		ProducerManifest: proto.Clone(s.producer).(*pb.ProducerManifest),
	}
	for _, outcome := range outcomes {
		if outcome.result == nil {
			return nil, status.Error(codes.Internal, "semantic worker did not produce an item result")
		}
		if math.MaxUint64-response.Usage.InputTokens < outcome.inputTokens || math.MaxUint64-response.Usage.OutputTokens < outcome.outputTokens {
			return nil, status.Error(codes.OutOfRange, "semantic token accounting overflow")
		}
		response.Usage.InputTokens += outcome.inputTokens
		response.Usage.OutputTokens += outcome.outputTokens
		response.Results = append(response.Results, outcome.result)
	}
	if err := domain.ValidateWire(response, domain.WireLimits{MaxBytes: s.config.MaximumInputBytes * 4, MaxDepth: 64, MaxItems: s.config.MaximumItems * 64}); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "validate semantic response: %v", err)
	}
	return response, nil
}

func itemError(itemID string, operation *pb.OperationError) *pb.ExtractItemResult {
	return &pb.ExtractItemResult{ItemId: itemID, Result: &pb.ExtractItemResult_Error{Error: operation}}
}

func contextError(err error) *pb.OperationError {
	code := pb.ErrorCode_ERROR_CODE_CANCELLED
	if errors.Is(err, context.DeadlineExceeded) {
		code = pb.ErrorCode_ERROR_CODE_DEADLINE_EXCEEDED
	}
	return &pb.OperationError{Code: code, SafeMessage: "semantic request context ended", Stage: "semantic.extract", Retryable: true}
}

func operationError(err error, itemID string) *pb.OperationError {
	var provider *ProviderError
	if errors.As(err, &provider) {
		code := pb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT
		if provider.Retryable {
			code = pb.ErrorCode_ERROR_CODE_UNAVAILABLE
		}
		return &pb.OperationError{Code: code, SafeMessage: provider.Safe, Stage: "semantic.extract", Retryable: provider.Retryable, ItemId: proto.String(itemID)}
	}
	return &pb.OperationError{Code: pb.ErrorCode_ERROR_CODE_INTERNAL, SafeMessage: "semantic provider failed", Stage: "semantic.extract", Retryable: false, ItemId: proto.String(itemID)}
}

type rawProposal struct {
	Mentions   *[]rawMention   `json:"mentions"`
	Assertions *[]rawAssertion `json:"assertions"`
	Supports   *[]rawSupport   `json:"supports"`
	Warnings   json.RawMessage `json:"warnings"`
}

type rawSpan struct {
	Start *uint64 `json:"start_byte"`
	End   *uint64 `json:"end_byte"`
	Quote string  `json:"quote"`
}

type rawMention struct {
	LocalID       string   `json:"local_id"`
	SurfaceForm   string   `json:"surface_form"`
	CandidateType string   `json:"candidate_type"`
	Span          *rawSpan `json:"span"`
}

type rawQualifier struct {
	PredicateID string          `json:"predicate_id"`
	ValueKind   string          `json:"value_kind"`
	Value       json.RawMessage `json:"value"`
}

type rawAssertion struct {
	LocalID           string          `json:"local_id"`
	SubjectLocalID    string          `json:"subject_local_id"`
	PredicateID       string          `json:"predicate_id"`
	ObjectLocalID     string          `json:"object_local_id"`
	Origin            string          `json:"origin"`
	Qualifiers        *[]rawQualifier `json:"qualifiers"`
	ExceptionLocalIDs *[]string       `json:"exception_local_ids"`
}

type rawSupport struct {
	AssertionLocalID string     `json:"assertion_local_id"`
	Spans            *[]rawSpan `json:"spans"`
}

func projectExtractionProposal(request *pb.ExtractBatchRequest, item *pb.TextItem, raw json.RawMessage, producer *pb.ProducerManifest, ontologyVersion string) (*pb.ExtractionProposal, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value rawProposal
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode strict extraction JSON: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, err
	}
	if value.Mentions == nil || value.Assertions == nil || value.Supports == nil {
		return nil, errors.New("mentions, assertions, and supports must be JSON arrays")
	}
	base := item.Provenance.Spans[0]
	mentionIDs := make(map[string]string, len(*value.Mentions))
	proposal := &pb.ExtractionProposal{}
	for index, mention := range *value.Mentions {
		if mention.LocalID == "" || mention.SurfaceForm == "" || mention.CandidateType == "" {
			return nil, errors.New("mention identity, surface, and type are required")
		}
		if _, duplicate := mentionIDs[mention.LocalID]; duplicate {
			return nil, errors.New("duplicate local mention ID")
		}
		if mention.Span == nil {
			return nil, errors.New("mention span is required")
		}
		span, err := absoluteSpan(item.Text, base, *mention.Span)
		if err != nil || mention.SurfaceForm != mention.Span.Quote {
			return nil, errors.New("mention surface does not match its exact UTF-8 byte span")
		}
		id := deterministicID("mention", request.Batch.OperationKey, item.ItemId, mention.LocalID, strconv.Itoa(index))
		mentionIDs[mention.LocalID] = id
		proposal.Mentions = append(proposal.Mentions, &pb.Mention{
			Meta: extractionMeta(request.Batch.Context.CorpusId, id), TextSpan: span,
			SourceRefs: cloneSourceRefs(item.Provenance.Sources), SurfaceForm: mention.SurfaceForm,
			CandidateType: mention.CandidateType, ExtractionManifest: proto.Clone(producer).(*pb.ProducerManifest),
		})
	}
	assertionIDs := make(map[string]string, len(*value.Assertions))
	for index, assertion := range *value.Assertions {
		if assertion.LocalID == "" {
			return nil, errors.New("assertion local ID is required")
		}
		if _, duplicate := assertionIDs[assertion.LocalID]; duplicate {
			return nil, errors.New("duplicate local assertion ID")
		}
		assertionIDs[assertion.LocalID] = deterministicID("assertion", request.Batch.OperationKey, item.ItemId, assertion.LocalID, strconv.Itoa(index))
	}
	for _, assertion := range *value.Assertions {
		subject, subjectOK := mentionIDs[assertion.SubjectLocalID]
		object, objectOK := mentionIDs[assertion.ObjectLocalID]
		if !subjectOK || !objectOK || assertion.PredicateID == "" {
			return nil, errors.New("assertion endpoint or predicate is unknown")
		}
		origin := pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT
		if assertion.Origin == "inferred" {
			origin = pb.AssertionOrigin_ASSERTION_ORIGIN_INFERRED
		} else if assertion.Origin != "explicit" {
			return nil, errors.New("assertion origin must be explicit or inferred")
		}
		if assertion.Qualifiers == nil || assertion.ExceptionLocalIDs == nil {
			return nil, errors.New("assertion qualifiers and exception_local_ids must be JSON arrays")
		}
		qualifiers := make([]*pb.Qualifier, 0, len(*assertion.Qualifiers))
		for _, qualifier := range *assertion.Qualifiers {
			mapped, err := projectQualifier(qualifier, mentionIDs)
			if err != nil {
				return nil, err
			}
			qualifiers = append(qualifiers, mapped)
		}
		exceptions := make([]string, 0, len(*assertion.ExceptionLocalIDs))
		seenExceptions := map[string]struct{}{}
		for _, local := range *assertion.ExceptionLocalIDs {
			mapped, ok := assertionIDs[local]
			if !ok || mapped == assertionIDs[assertion.LocalID] {
				return nil, errors.New("assertion exception reference is unknown or self-referential")
			}
			if _, duplicate := seenExceptions[mapped]; duplicate {
				return nil, errors.New("duplicate assertion exception reference")
			}
			seenExceptions[mapped] = struct{}{}
			exceptions = append(exceptions, mapped)
		}
		proposal.Assertions = append(proposal.Assertions, &pb.RelationAssertion{
			Meta:      extractionMeta(request.Batch.Context.CorpusId, assertionIDs[assertion.LocalID]),
			SubjectId: subject, PredicateId: assertion.PredicateID, ObjectId: object,
			Qualifiers: qualifiers, ExceptionRefs: exceptions,
			TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REQUIRE_REVIEW},
			Origin:        origin, OntologyVersion: ontologyVersion,
		})
	}
	supportCounts := make(map[string]int, len(assertionIDs))
	for index, support := range *value.Supports {
		assertionID, ok := assertionIDs[support.AssertionLocalID]
		if !ok || support.Spans == nil || len(*support.Spans) == 0 {
			return nil, errors.New("support references an unknown assertion or has no span")
		}
		spans := make([]*pb.TextSpan, 0, len(*support.Spans))
		for _, rawSpan := range *support.Spans {
			span, err := absoluteSpan(item.Text, base, rawSpan)
			if err != nil {
				return nil, fmt.Errorf("invalid support span: %w", err)
			}
			spans = append(spans, span)
		}
		proposal.Supports = append(proposal.Supports, &pb.SupportRecord{
			Meta:        extractionMeta(request.Batch.Context.CorpusId, deterministicID("support", request.Batch.OperationKey, item.ItemId, support.AssertionLocalID, strconv.Itoa(index))),
			AssertionId: assertionID, EvidenceSpans: spans, SourceRefs: cloneSourceRefs(item.Provenance.Sources),
			ExtractionManifest:     proto.Clone(producer).(*pb.ProducerManifest),
			IndependentSourceGroup: independentSourceGroup(item.Provenance.Sources), ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED,
		})
		supportCounts[assertionID]++
	}
	for _, assertionID := range assertionIDs {
		if supportCounts[assertionID] == 0 {
			return nil, errors.New("every assertion requires exact source support")
		}
	}
	warnings := []string{}
	if len(value.Warnings) > 0 {
		if err := decodeStrict(value.Warnings, &warnings); err != nil || warnings == nil {
			return nil, errors.New("warnings must be a JSON array when present")
		}
	}
	for _, warning := range warnings {
		if warning == "" {
			return nil, errors.New("empty model warning")
		}
		proposal.Issues = append(proposal.Issues, &pb.ValidationIssue{
			Code: "MODEL_WARNING_" + sha256String([]byte(warning))[:12], Severity: pb.Severity_SEVERITY_WARNING, RecordId: item.ItemId,
			FieldPath: "structured_output", Disposition: "review",
		})
	}
	return proposal, nil
}

func absoluteSpan(text string, base *pb.TextSpan, relative rawSpan) (*pb.TextSpan, error) {
	if relative.Start == nil || relative.End == nil {
		return nil, errors.New("span start_byte and end_byte are required")
	}
	startRelative, endRelative := *relative.Start, *relative.End
	if startRelative >= endRelative || endRelative > uint64(len(text)) || endRelative-startRelative != uint64(len(relative.Quote)) ||
		!utf8.ValidString(relative.Quote) || !utf8.RuneStart(text[startRelative]) || (endRelative < uint64(len(text)) && !utf8.RuneStart(text[endRelative])) ||
		text[startRelative:endRelative] != relative.Quote {
		return nil, errors.New("span is not an exact UTF-8 byte slice")
	}
	if math.MaxUint64-base.StartByte < startRelative || math.MaxUint64-base.StartByte < endRelative {
		return nil, errors.New("absolute span overflows")
	}
	start, end := base.StartByte+startRelative, base.StartByte+endRelative
	if end > base.EndByte || start < base.StartByte {
		return nil, errors.New("span escapes source item")
	}
	return &pb.TextSpan{TextArtifactId: base.TextArtifactId, StartByte: start, EndByte: end}, nil
}

func projectQualifier(raw rawQualifier, mentions map[string]string) (*pb.Qualifier, error) {
	if raw.PredicateID == "" {
		return nil, errors.New("qualifier predicate is required")
	}
	if len(raw.Value) == 0 || bytes.Equal(bytes.TrimSpace(raw.Value), []byte("null")) {
		return nil, errors.New("qualifier value is required")
	}
	qualifier := &pb.Qualifier{PredicateId: raw.PredicateID}
	switch raw.ValueKind {
	case "mention":
		var local string
		if err := decodeStrict(raw.Value, &local); err != nil {
			return nil, errors.New("invalid mention qualifier")
		}
		mapped, ok := mentions[local]
		if !ok {
			return nil, errors.New("qualifier references unknown mention")
		}
		qualifier.Value = &pb.Qualifier_MentionId{MentionId: mapped}
	case "literal":
		var literal string
		if err := decodeStrict(raw.Value, &literal); err != nil || literal == "" {
			return nil, errors.New("invalid literal qualifier")
		}
		qualifier.Value = &pb.Qualifier_Literal{Literal: literal}
	case "number":
		var number float64
		if err := decodeStrict(raw.Value, &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, errors.New("invalid number qualifier")
		}
		qualifier.Value = &pb.Qualifier_Number{Number: number}
	case "date":
		var date struct {
			Year  int32  `json:"year"`
			Month uint32 `json:"month"`
			Day   uint32 `json:"day"`
		}
		if err := decodeStrict(raw.Value, &date); err != nil {
			return nil, errors.New("invalid date qualifier")
		}
		qualifier.Value = &pb.Qualifier_Date{Date: &pb.CalendarDate{Year: date.Year, Month: date.Month, Day: date.Day}}
	default:
		return nil, errors.New("unknown qualifier value kind")
	}
	return qualifier, nil
}

func decodeStrict(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return rejectTrailingJSON(decoder)
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("structured output has trailing JSON")
	}
	return nil
}

func extractionMeta(corpusID, recordID string) *pb.RecordMeta {
	return &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: recordID}
}

func cloneSourceRefs(values []*pb.SourceVersionRef) []*pb.SourceVersionRef {
	result := make([]*pb.SourceVersionRef, len(values))
	for index, value := range values {
		result[index] = proto.Clone(value).(*pb.SourceVersionRef)
	}
	return result
}

func deterministicID(kind string, parts ...string) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(kind))
	for _, part := range parts {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(part))
	}
	return kind + ":" + hex.EncodeToString(hasher.Sum(nil))
}

func independentSourceGroup(sources []*pb.SourceVersionRef) string {
	keys := make([]string, 0, len(sources))
	for _, source := range sources {
		keys = append(keys, source.SourceBlobId)
	}
	sort.Strings(keys)
	return deterministicID("source-group", keys...)
}

func sha256String(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func semanticRequestFingerprint(request *pb.ExtractBatchRequest) ([32]byte, error) {
	clone := proto.Clone(request).(*pb.ExtractBatchRequest)
	clone.Batch.Context.RequestId = ""
	clone.Batch.Context.TraceId = ""
	clone.Batch.Context.Deadline = nil
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

type cachedSemantic struct {
	fingerprint [32]byte
	response    *pb.ExtractBatchResponse
	items       map[string]itemOutcome
	sizeBytes   int64
}
type inflightSemantic struct {
	fingerprint [32]byte
	done        chan struct{}
	response    *pb.ExtractBatchResponse
	err         error
}
type semanticCache struct {
	mu           sync.Mutex
	maximum      int
	maximumBytes int64
	currentBytes int64
	entries      map[string]*cachedSemantic
	order        []string
	inflight     map[string]*inflightSemantic
}

func newSemanticCache(maximum int, maximumBytes int64) *semanticCache {
	return &semanticCache{
		maximum: maximum, maximumBytes: maximumBytes,
		entries: map[string]*cachedSemantic{}, inflight: map[string]*inflightSemantic{},
	}
}

func (c *semanticCache) execute(ctx context.Context, key string, fingerprint [32]byte, run func() (*pb.ExtractBatchResponse, error)) (*pb.ExtractBatchResponse, error) {
	for {
		c.mu.Lock()
		if entry, ok := c.entries[key]; ok {
			if entry.fingerprint != fingerprint {
				c.mu.Unlock()
				return nil, status.Error(codes.FailedPrecondition, "operation key was reused for different semantic input")
			}
			if entry.response != nil {
				response := proto.Clone(entry.response).(*pb.ExtractBatchResponse)
				c.mu.Unlock()
				return response, nil
			}
		} else {
			c.entries[key] = &cachedSemantic{fingerprint: fingerprint, items: map[string]itemOutcome{}}
			c.order = append(c.order, key)
		}
		if active, ok := c.inflight[key]; ok {
			if active.fingerprint != fingerprint {
				c.mu.Unlock()
				return nil, status.Error(codes.FailedPrecondition, "operation key collided with active semantic input")
			}
			done := active.done
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			case <-done:
			}
			if active.err != nil {
				return nil, active.err
			}
			return proto.Clone(active.response).(*pb.ExtractBatchResponse), nil
		}
		active := &inflightSemantic{fingerprint: fingerprint, done: make(chan struct{})}
		c.inflight[key] = active
		c.mu.Unlock()
		response, err := run()
		c.mu.Lock()
		active.err = err
		if response != nil {
			active.response = proto.Clone(response).(*pb.ExtractBatchResponse)
		}
		if err == nil && semanticResponseCacheable(response) {
			entry := c.entries[key]
			c.currentBytes -= entry.sizeBytes
			entry.response = proto.Clone(response).(*pb.ExtractBatchResponse)
			entry.items = nil
			entry.sizeBytes = int64(proto.Size(entry.response))
			c.currentBytes += entry.sizeBytes
		}
		delete(c.inflight, key)
		close(active.done)
		c.evictLocked()
		c.mu.Unlock()
		if response == nil {
			return nil, err
		}
		return proto.Clone(response).(*pb.ExtractBatchResponse), err
	}
}

func (c *semanticCache) loadItem(operationKey, itemID string, fingerprint [32]byte) (itemOutcome, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[operationKey]
	if !ok || entry.fingerprint != fingerprint || entry.items == nil {
		return itemOutcome{}, false
	}
	outcome, ok := entry.items[itemID]
	if !ok {
		return itemOutcome{}, false
	}
	return cloneItemOutcome(outcome), true
}

func (c *semanticCache) storeItem(operationKey, itemID string, fingerprint [32]byte, outcome itemOutcome) {
	if outcome.result == nil {
		return
	}
	if operation := outcome.result.GetError(); operation != nil && operation.Retryable {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[operationKey]
	if !ok || entry.fingerprint != fingerprint || entry.response != nil {
		return
	}
	if _, exists := entry.items[itemID]; exists {
		return
	}
	cloned := cloneItemOutcome(outcome)
	entry.items[itemID] = cloned
	size := int64(proto.Size(cloned.result)) + 16
	entry.sizeBytes += size
	c.currentBytes += size
}

func cloneItemOutcome(outcome itemOutcome) itemOutcome {
	cloned := outcome
	if outcome.result != nil {
		cloned.result = proto.Clone(outcome.result).(*pb.ExtractItemResult)
	}
	return cloned
}

func (c *semanticCache) evictLocked() {
	remaining := len(c.order)
	for (len(c.entries) > c.maximum || c.currentBytes > c.maximumBytes) && remaining > 0 && len(c.order) > 0 {
		key := c.order[0]
		c.order = c.order[1:]
		if _, active := c.inflight[key]; active {
			c.order = append(c.order, key)
			remaining--
			continue
		}
		entry, ok := c.entries[key]
		if ok {
			c.currentBytes -= entry.sizeBytes
			delete(c.entries, key)
		}
		remaining = len(c.order)
	}
}

func semanticResponseCacheable(response *pb.ExtractBatchResponse) bool {
	for _, result := range response.Results {
		if operation := result.GetError(); operation != nil && operation.Retryable {
			return false
		}
	}
	return true
}
