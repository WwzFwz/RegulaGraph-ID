// Persists bounded EXTRACT provider completions for replay after process restart.
// INSERT ON CONFLICT plus a subsequent read returns the first committed value;
// no transaction/connection is held during inference. Payload and token usage are
// hashed together and checked on every load. These rows confer no registry/job or
// publication authority. Measure DB latency, retained bytes and duplicate sampling
// under concurrent misses; benchmark-targets.yaml remains the required source.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) LoadModelCompletion(ctx context.Context, key *pb.ContentHash) (domain.ModelCompletion, error) {
	if r == nil || r.pool == nil {
		return domain.ModelCompletion{}, errors.New("completion repository unavailable")
	}
	if key == nil {
		return domain.ModelCompletion{}, errors.New("completion key required")
	}
	if err := domain.ValidateWire(key, domain.DefaultWireLimits); err != nil {
		return domain.ModelCompletion{}, err
	}
	var value domain.ModelCompletion
	var input, output int64
	var digest string
	// Bound transfer even if a privileged mutation bypassed the table constraint.
	err := r.pool.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload) <= $2 THEN payload ELSE NULL END,
        input_tokens, output_tokens, payload_hash FROM semantic_completions WHERE replay_key=$1`, key.Sha256, domain.MaximumModelCompletionBytes).Scan(&value.JSON, &input, &output, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, domain.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	if input < 0 || output < 0 {
		return domain.ModelCompletion{}, domain.ErrPersistentIntegrity
	}
	value.InputTokens, value.OutputTokens = uint64(input), uint64(output)
	hash, err := domain.ModelCompletionCheckpointHash(key, value)
	if err != nil || hash.Sha256 != digest {
		return domain.ModelCompletion{}, domain.ErrPersistentIntegrity
	}
	return value, nil
}

func (r *Repository) SaveModelCompletion(ctx context.Context, key *pb.ContentHash, value domain.ModelCompletion) (domain.ModelCompletion, error) {
	if r == nil || r.pool == nil {
		return domain.ModelCompletion{}, errors.New("completion repository unavailable")
	}
	if key == nil {
		return domain.ModelCompletion{}, errors.New("completion key required")
	}
	if err := domain.ValidateWire(key, domain.DefaultWireLimits); err != nil {
		return domain.ModelCompletion{}, err
	}
	hash, err := domain.ModelCompletionCheckpointHash(key, value)
	if err != nil {
		return domain.ModelCompletion{}, err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO semantic_completions(replay_key,payload,input_tokens,output_tokens,payload_hash)
        VALUES($1,$2,$3,$4,$5) ON CONFLICT(replay_key) DO NOTHING`, key.Sha256, []byte(value.JSON), int64(value.InputTokens), int64(value.OutputTokens), hash.Sha256)
	if err != nil {
		return domain.ModelCompletion{}, err
	}
	// A separate statement observes a concurrent winning insert after it commits.
	return r.LoadModelCompletion(ctx, key)
}
