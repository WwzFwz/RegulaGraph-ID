// Replays immutable EXTRACT provider completions across gateway restarts. Keys
// bind the complete semantic request (including corpus/scope/source provenance),
// actual producer and exact encoded chat envelope; request IDs/deadlines are
// excluded by semanticRequestFingerprint. Replay ALWAYS goes through projection,
// ontology/evidence/C01 validation again. It is not a registry/publication commit.
// Save precedes acknowledgement; concurrent misses can sample twice but all
// callers use the first committed completion. Measure storage latency, replay hit
// rate and duplicate sampling; no quality/performance acceptance is implied.
package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (s *SemanticService) generateExtract(ctx context.Context, fingerprint [32]byte, request StructuredRequest) (StructuredResponse, error) {
	store := s.config.ExtractionStore
	if store == nil {
		return s.provider.Generate(ctx, request)
	}
	body, err := encodeStructuredChat(request)
	if err != nil {
		return StructuredResponse{}, err
	}
	producer, err := proto.MarshalOptions{Deterministic: true}.Marshal(s.producer)
	if err != nil {
		return StructuredResponse{}, err
	}
	h := sha256.New()
	h.Write([]byte("regulagraph-extract-completion-key-v1\x00"))
	h.Write(fingerprint[:])
	producerHash := sha256.Sum256(producer)
	h.Write(producerHash[:])
	h.Write(body)
	key := &pb.ContentHash{Sha256: hex.EncodeToString(h.Sum(nil))}
	result, err := store.LoadModelCompletion(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		result, err = s.provider.Generate(ctx, request)
		if err != nil {
			return StructuredResponse{}, err
		}
		if _, err = domain.ModelCompletionHash(result); err != nil {
			return StructuredResponse{}, err
		}
		result, err = store.SaveModelCompletion(ctx, key, result)
	}
	if err != nil {
		return StructuredResponse{}, &ProviderError{Code: "completion_replay", Safe: "semantic completion checkpoint unavailable or invalid", Retryable: !errors.Is(err, domain.ErrPersistentIntegrity), cause: err}
	}
	if _, err = domain.ModelCompletionHash(result); err != nil || result.OutputTokens > uint64(request.MaxOutputTokens) {
		return StructuredResponse{}, &ProviderError{Code: "completion_replay_integrity", Safe: "semantic completion checkpoint violates runtime bounds"}
	}
	return result, ctx.Err()
}
