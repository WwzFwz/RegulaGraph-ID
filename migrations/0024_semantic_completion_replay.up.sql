-- Immutable provider completions for EXTRACT restart recovery. Keys bind scope,
-- request/input and actual producer; values are NOT graph/publication receipts.
-- No source/job rows are changed and no backfill is performed. Apply before
-- enabling gateway PostgreSQL replay; retention/GC remains a separate policy.
CREATE TABLE semantic_completions (
    replay_key text PRIMARY KEY CHECK(replay_key ~ '^[0-9a-f]{64}$'),
    payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 4194304),
    input_tokens bigint NOT NULL CHECK(input_tokens >= 0),
    output_tokens bigint NOT NULL CHECK(output_tokens >= 0),
    payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER semantic_completions_immutable BEFORE UPDATE OR DELETE ON semantic_completions
    FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
