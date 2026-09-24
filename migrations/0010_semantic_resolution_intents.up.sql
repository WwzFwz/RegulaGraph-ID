-- K01 stores one immutable, hash-checked RESOLVE intent before registry CAS so a lease
-- successor can replay the exact request, review assertions, model, and output identity after
-- a crash before terminal checkpoint. New table/unique indexes require rollout lock measurement;
-- no historic rows are synthesized. Required latency targets remain REQUIRED_UNMEASURED.
CREATE TABLE semantic_resolution_intents (
    job_id text PRIMARY KEY REFERENCES jobs(job_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    source_checkpoint_id text NOT NULL REFERENCES job_checkpoints(checkpoint_id),
    operation_key text NOT NULL CHECK (operation_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    payload bytea NOT NULL CHECK (octet_length(payload) > 0 AND octet_length(payload) <= 16777216),
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (corpus_id, operation_key)
);

CREATE TRIGGER semantic_resolution_intents_append_only
    BEFORE UPDATE OR DELETE ON semantic_resolution_intents
    FOR EACH ROW EXECUTE FUNCTION reject_semantic_ledger_rewrite();
