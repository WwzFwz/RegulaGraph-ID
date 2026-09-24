// Implements Semantic.ResolveBatch using a pinned prompt/model and verified source excerpts.
// The provider judges identity among supplied candidates; Go owns IDs, revision, provenance,
// and LINK/DEFER projection. A proposal never authorizes a registry write or human approval.
// Bounded admission, parallel calls, and terminal-item reuse limit cost and latency. Measure
// candidate recall, false merges/splits, queue/service p95/p99, tokens and bytes under
// configs/benchmark-targets.yaml; fixture tests do not establish model quality.
package inference

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (s *SemanticService) ResolveBatch(ctx context.Context, request *pb.SemanticResolveRequest) (*pb.SemanticResolveResponse, error) {
	if s.config.Model.Task != pb.ModelTask_MODEL_TASK_RESOLVE {
		return nil, status.Error(codes.FailedPrecondition, "gateway is not configured for RESOLVE")
	}
	if err := s.validateResolveRequest(request); err != nil {
		return nil, err
	}
	execution, cancel := context.WithDeadline(ctx, request.Batch.Context.Deadline.AsTime())
	defer cancel()
	if err := execution.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	select {
	case s.operations <- struct{}{}:
		defer func() { <-s.operations }()
	default:
		return nil, status.Error(codes.ResourceExhausted, "semantic operation capacity is full")
	}
	stable := proto.Clone(request).(*pb.SemanticResolveRequest)
	stable.Batch.Context.RequestId = ""
	stable.Batch.Context.TraceId = ""
	stable.Batch.Context.Deadline = nil
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(stable)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot fingerprint resolution input")
	}
	fingerprint := sha256.Sum256(raw)
	response, err := s.resolutionCache.execute(execution, request.Batch.OperationKey, fingerprint,
		func(cached map[string]resolveOutcome) (*pb.SemanticResolveResponse, map[string]resolveOutcome, error) {
			return s.executeResolve(execution, request, cached)
		})
	if err != nil {
		return nil, err
	}
	response.RequestId = request.Batch.Context.RequestId
	return response, nil
}

func (s *SemanticService) validateResolveRequest(request *pb.SemanticResolveRequest) error {
	limits := domain.WireLimits{MaxBytes: s.config.MaximumInputBytes, MaxDepth: 64, MaxItems: s.config.MaximumItems * 64}
	if err := domain.ValidateWire(request, limits); err != nil {
		return status.Errorf(codes.InvalidArgument, "validate ResolveBatch: %v", err)
	}
	batch := request.Batch
	if batch == nil || batch.Context == nil || batch.Context.Deadline == nil ||
		batch.Context.Deadline.CheckValid() != nil {
		return status.Error(codes.InvalidArgument, "resolution context/deadline is incomplete")
	}
	if !proto.Equal(batch.Model, s.config.Model) || batch.OntologyVersion != s.config.Ontology.Version() ||
		batch.GetOutputSchema().GetMediaType() != "application/schema+json" ||
		batch.GetOutputSchema().GetSchemaVersion() != 1 ||
		!proto.Equal(batch.GetOutputSchema().GetContentHash(), s.config.OutputSchemaHash) ||
		batch.OutputSchema.ByteSize != uint64(len(s.config.OutputSchema)) {
		return status.Error(codes.FailedPrecondition, "resolution model, ontology, or schema differs from pinned runtime")
	}
	if len(request.Items) == 0 || len(request.Items) > s.config.MaximumItems {
		return status.Error(codes.ResourceExhausted, "resolution item count exceeds budget")
	}
	seen, mentions := map[string]bool{}, map[string]bool{}
	for _, item := range request.Items {
		if item == nil || item.Mention == nil || item.Mention.Meta == nil ||
			seen[item.ItemId] || mentions[item.Mention.Meta.RecordId] {
			return status.Error(codes.InvalidArgument, "resolution items/mentions must be present and unique")
		}
		seen[item.ItemId], mentions[item.Mention.Meta.RecordId] = true, true
		if err := validateResolutionContext(item, batch.Context.CorpusId); err != nil {
			return status.Errorf(codes.InvalidArgument, "resolution context: %v", err)
		}
	}
	return nil
}

// Context is an internal trusted-caller boundary: the workflow verifies artifact hashes.
// Here we reject inconsistent source/version IDs, malformed spans, and forged mention bytes.
func validateResolutionContext(item *pb.AmbiguousMention, corpus string) error {
	mention := item.Mention
	if mention.Meta.CorpusId != corpus || item.ExpectedRegistryRevision == 0 ||
		len(item.ContextItems) == 0 || mention.TextSpan == nil {
		return errors.New("matching corpus, registry revision and hydrated context are required")
	}
	expected := &pb.Provenance{Sources: mention.SourceRefs, Spans: []*pb.TextSpan{mention.TextSpan}}
	if !proto.Equal(item.Evidence, expected) {
		return errors.New("proposal evidence must preserve the exact mention provenance")
	}
	candidates := map[string]bool{}
	for _, candidate := range item.Candidates {
		if candidate == nil || candidate.Meta == nil || candidate.Meta.CorpusId != corpus ||
			candidate.EntityType != mention.CandidateType || candidate.RegistryRevision == 0 ||
			candidate.RegistryRevision > item.ExpectedRegistryRevision || candidates[candidate.Meta.RecordId] {
			return errors.New("candidate identity, type, corpus or revision differs")
		}
		candidates[candidate.Meta.RecordId] = true
	}
	seen := map[string]bool{}
	covered := false
	for _, excerpt := range item.ContextItems {
		if excerpt == nil || excerpt.ItemId == "" || seen[excerpt.ItemId] || excerpt.Text == "" ||
			!utf8.ValidString(excerpt.Text) || excerpt.Provenance == nil ||
			len(excerpt.Provenance.Spans) != 1 || len(excerpt.Provenance.Sources) == 0 {
			return errors.New("context requires unique IDs, UTF-8 text and source provenance")
		}
		seen[excerpt.ItemId] = true
		span := excerpt.Provenance.Spans[0]
		if span == nil || span.EndByte < span.StartByte || span.EndByte-span.StartByte != uint64(len(excerpt.Text)) {
			return errors.New("context span does not cover its UTF-8 bytes")
		}
		// Structural chunks can span several provision versions in the same source document.
		// The workflow verifies each chunk/version binding from its hashed DocumentBatch.
		for _, source := range excerpt.Provenance.Sources {
			found := false
			for _, original := range mention.SourceRefs {
				if source.SourceBlobId == original.SourceBlobId {
					found = true
					break
				}
			}
			if !found {
				return errors.New("context source document is outside the mention evidence")
			}
		}
		target := mention.TextSpan
		if span.TextArtifactId == target.TextArtifactId && span.StartByte <= target.StartByte && span.EndByte >= target.EndByte {
			start, end := target.StartByte-span.StartByte, target.EndByte-span.StartByte
			if end < start || !utf8.ValidString(excerpt.Text[start:end]) || excerpt.Text[start:end] != mention.SurfaceForm {
				return errors.New("mention does not match context bytes")
			}
			covered = true
		}
	}
	if !covered {
		return errors.New("context does not contain the mention")
	}
	return nil
}

type resolveOutcome struct {
	result                    *pb.ResolveItemResult
	inputTokens, outputTokens uint64
	queueNs, providerNs       uint64
}

func (s *SemanticService) executeResolve(ctx context.Context, request *pb.SemanticResolveRequest,
	cached map[string]resolveOutcome) (*pb.SemanticResolveResponse, map[string]resolveOutcome, error) {
	started := time.Now()
	outcomes := make([]resolveOutcome, len(request.Items))
	var wait sync.WaitGroup
	for index, item := range request.Items {
		if previous, ok := cached[item.ItemId]; ok {
			outcomes[index] = previous
			continue
		}
		wait.Add(1)
		go func(index int, item *pb.AmbiguousMention) {
			defer wait.Done()
			queued := time.Now()
			select {
			case s.semaphore <- struct{}{}:
				defer func() { <-s.semaphore }()
			case <-ctx.Done():
				outcomes[index].result = resolveError(item.ItemId, contextError(ctx.Err()))
				return
			}
			outcomes[index].queueNs = uint64(time.Since(queued))
			payload, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(item)
			if err != nil {
				if ctx.Err() != nil {
					outcomes[index].result = resolveError(item.ItemId, contextError(ctx.Err()))
					return
				}
				outcomes[index].result = resolveError(item.ItemId, operationError(err, item.ItemId))
				return
			}
			providerStarted := time.Now()
			generated, err := s.provider.Generate(ctx, StructuredRequest{
				ModelID: s.config.Model.ModelId, SystemPrompt: s.config.SystemPrompt, ItemID: item.ItemId,
				Text: string(payload), SchemaName: s.config.SchemaName, Schema: s.config.OutputSchema,
			})
			outcomes[index].providerNs = uint64(time.Since(providerStarted))
			outcomes[index].inputTokens, outcomes[index].outputTokens = generated.InputTokens, generated.OutputTokens
			if err != nil {
				if ctx.Err() != nil {
					outcomes[index].result = resolveError(item.ItemId, contextError(ctx.Err()))
					return
				}
				outcomes[index].result = resolveError(item.ItemId, operationError(err, item.ItemId))
				return
			}
			if len(generated.JSON) > s.config.MaximumInputBytes {
				outcomes[index].result = resolveError(item.ItemId, &pb.OperationError{Code: pb.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED, SafeMessage: "resolution output exceeds budget"})
				return
			}
			proposal, err := projectResolution(request, item, generated.JSON)
			if err != nil {
				outcomes[index].result = resolveError(item.ItemId, &pb.OperationError{
					Code: pb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, SafeMessage: "provider output failed resolution validation"})
				return
			}
			outcomes[index].result = &pb.ResolveItemResult{ItemId: item.ItemId, Result: &pb.ResolveItemResult_Proposal{Proposal: proposal}}
		}(index, item)
	}
	wait.Wait()
	retained := map[string]resolveOutcome{}
	response := &pb.SemanticResolveResponse{RequestId: request.Batch.Context.RequestId,
		Model:            proto.Clone(s.config.Model).(*pb.ModelManifest),
		ProducerManifest: proto.Clone(s.producer).(*pb.ProducerManifest),
		Usage:            &pb.TokenUsage{TokenizerId: s.config.TokenizerID},
		Durations:        []*pb.StageDuration{{Stage: "semantic_resolve_total", DurationNs: uint64(time.Since(started))}},
	}
	for index, outcome := range outcomes {
		if outcome.result == nil {
			return nil, retained, status.Error(codes.Internal, "resolution item missing")
		}
		if operation := outcome.result.GetError(); operation == nil || !operation.Retryable {
			retained[request.Items[index].ItemId] = outcome
		}
		if math.MaxUint64-response.Usage.InputTokens < outcome.inputTokens || math.MaxUint64-response.Usage.OutputTokens < outcome.outputTokens {
			return nil, retained, status.Error(codes.OutOfRange, "resolution token accounting overflow")
		}
		response.Usage.InputTokens += outcome.inputTokens
		response.Usage.OutputTokens += outcome.outputTokens
		response.Results = append(response.Results, outcome.result)
		response.Durations = append(response.Durations,
			&pb.StageDuration{Stage: "semantic_resolve_queue:" + request.Items[index].ItemId, DurationNs: outcome.queueNs},
			&pb.StageDuration{Stage: "semantic_resolve_provider:" + request.Items[index].ItemId, DurationNs: outcome.providerNs})
	}
	if ctx.Err() != nil {
		return nil, retained, status.FromContextError(ctx.Err()).Err()
	}
	if err := domain.ValidateWire(response, domain.DefaultWireLimits); err != nil {
		return nil, retained, status.Errorf(codes.Internal, "invalid resolution response: %v", err)
	}
	return response, retained, nil
}

func resolveError(id string, operation *pb.OperationError) *pb.ResolveItemResult {
	operation.Stage = "semantic.resolve"
	operation.ItemId = proto.String(id)
	return &pb.ResolveItemResult{ItemId: id, Result: &pb.ResolveItemResult_Error{Error: operation}}
}

func projectResolution(request *pb.SemanticResolveRequest, item *pb.AmbiguousMention, raw json.RawMessage) (*pb.ResolutionProposal, error) {
	if err := validateResolutionJSONKeys(raw); err != nil {
		return nil, err
	}
	var output struct {
		Action      string   `json:"action"`
		CandidateID string   `json:"candidate_id"`
		Rationale   string   `json:"rationale"`
		EvidenceIDs []string `json:"evidence_item_ids"`
	}
	if err := decodeStrict(raw, &output); err != nil {
		return nil, err
	}
	if strings.TrimSpace(output.Rationale) == "" || len(output.EvidenceIDs) == 0 {
		return nil, errors.New("resolution requires rationale and evidence")
	}
	evidence := map[string]bool{}
	for _, context := range item.ContextItems {
		evidence[context.ItemId] = true
	}
	seen := map[string]bool{}
	mentionContext := false
	for _, id := range output.EvidenceIDs {
		if !evidence[id] || seen[id] {
			return nil, errors.New("unknown or duplicate resolution evidence")
		}
		seen[id] = true
		for _, excerpt := range item.ContextItems {
			span := excerpt.Provenance.Spans[0]
			if excerpt.ItemId == id && span.TextArtifactId == item.Mention.TextSpan.TextArtifactId &&
				span.StartByte <= item.Mention.TextSpan.StartByte && span.EndByte >= item.Mention.TextSpan.EndByte {
				mentionContext = true
			}
		}
	}
	if !mentionContext {
		return nil, errors.New("resolution must cite the mention context")
	}
	proposal := &pb.ResolutionProposal{
		Meta:       extractionMeta(request.Batch.Context.CorpusId, deterministicID("resolution", request.Batch.OperationKey, item.ItemId)),
		MentionIds: []string{item.Mention.Meta.RecordId}, Evidence: proto.Clone(item.Evidence).(*pb.Provenance),
		ExpectedRegistryRevision: item.ExpectedRegistryRevision, LocalCorrelationId: item.ItemId,
		Method: "llm_context_resolution_v1", Rationale: proto.String(output.Rationale),
		SupportingContextIds: append([]string(nil), output.EvidenceIDs...),
	}
	switch output.Action {
	case "LINK":
		found := false
		for _, candidate := range item.Candidates {
			if candidate.Meta.RecordId == output.CandidateID {
				found = true
			}
		}
		if !found {
			return nil, errors.New("model selected an unknown candidate")
		}
		proposal.Action = pb.ResolutionAction_RESOLUTION_ACTION_LINK
		proposal.CandidateIds = []string{output.CandidateID}
	case "DEFER":
		if output.CandidateID != "" {
			return nil, errors.New("deferred resolution cannot select a canonical")
		}
		proposal.Action = pb.ResolutionAction_RESOLUTION_ACTION_DEFER
		for _, candidate := range item.Candidates {
			proposal.CandidateIds = append(proposal.CandidateIds, candidate.Meta.RecordId)
		}
	default:
		return nil, fmt.Errorf("unsupported resolution action %q", output.Action)
	}
	if err := domain.ValidateWire(proposal, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	return proposal, nil
}

// Provider schemas are advisory at this boundary: enforce required non-null keys locally
// and reject duplicate keys rather than allowing ambiguous last-wins JSON interpretation.
func validateResolutionJSONKeys(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("resolution output must be an object")
	}
	required := map[string]bool{"action": false, "candidate_id": false, "rationale": false, "evidence_item_ids": false}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		seen, known := required[key]
		if err != nil || !ok || !known || seen {
			return errors.New("resolution JSON has unknown or duplicate key")
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("resolution fields must be present and non-null")
		}
		required[key] = true
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	for _, seen := range required {
		if !seen {
			return errors.New("resolution JSON is missing a required field")
		}
	}
	return rejectTrailingJSON(decoder)
}
