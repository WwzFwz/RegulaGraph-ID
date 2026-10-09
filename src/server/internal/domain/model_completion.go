// Defines bounded, immutable model-completion replay data shared by Go adapters.
// A completion is provider JSON and historical token usage, not a validated graph
// fact or publication receipt. Callers bind keys to scope/input/producer and rerun
// semantic validation after reading. Measure storage latency/bytes and replay hit
// rate against benchmark-targets.yaml; this DTO is not a new cross-runtime schema.
package domain

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"unicode/utf8"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const MaximumModelCompletionBytes = 4 << 20

type ModelCompletion struct {
	JSON         json.RawMessage
	InputTokens  uint64
	OutputTokens uint64
}

type ModelCompletionStore interface {
	LoadModelCompletion(context.Context, *pb.ContentHash) (ModelCompletion, error)
	// Save returns the first committed value, including when another writer won.
	SaveModelCompletion(context.Context, *pb.ContentHash, ModelCompletion) (ModelCompletion, error)
}

func ModelCompletionHash(value ModelCompletion) (*pb.ContentHash, error) {
	if len(value.JSON) == 0 || len(value.JSON) > MaximumModelCompletionBytes || !utf8.Valid(value.JSON) || !json.Valid(value.JSON) || value.InputTokens > math.MaxInt64 || value.OutputTokens > math.MaxInt64 {
		return nil, errors.New("model completion exceeds byte/token bounds or contains invalid JSON")
	}
	h := sha256.New()
	h.Write([]byte("regulagraph-model-completion-v1\x00"))
	var usage [16]byte
	binary.BigEndian.PutUint64(usage[:8], value.InputTokens)
	binary.BigEndian.PutUint64(usage[8:], value.OutputTokens)
	h.Write(usage[:])
	h.Write(value.JSON)
	return &pb.ContentHash{Sha256: hex.EncodeToString(h.Sum(nil))}, nil
}

// Bind the payload/usage digest to its replay identity so a valid row cannot be
// copied under a different key without failing the integrity check.
func ModelCompletionCheckpointHash(key *pb.ContentHash, value ModelCompletion) (*pb.ContentHash, error) {
	if key == nil {
		return nil, errors.New("completion replay key required")
	}
	if err := ValidateWire(key, DefaultWireLimits); err != nil {
		return nil, err
	}
	content, err := ModelCompletionHash(value)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte("regulagraph-model-completion-binding-v1\x00" + key.Sha256 + content.Sha256))
	return &pb.ContentHash{Sha256: hex.EncodeToString(digest[:])}, nil
}
