-- Records explicit operator approval of the entire immutable proposal batch. The existing
-- LINK review ledger and resolution intent are written in the same transaction as this row
-- and WAITING_REVIEW -> RETRY_WAIT. No backfill or automatic approvals. Apply before the
-- review CLI; retry the same checksummed migration on failure. Production lock/latency
-- targets remain REQUIRED_UNMEASURED in configs/benchmark-targets.yaml.
CREATE TABLE semantic_batch_reviews (
    job_id text PRIMARY KEY REFERENCES semantic_proposal_queue(job_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    actor text NOT NULL CHECK (actor ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    reason text NOT NULL CHECK (octet_length(reason) BETWEEN 1 AND 1024),
    output_hash text NOT NULL CHECK (output_hash ~ '^[0-9a-f]{64}$'),
    expected_revision bigint NOT NULL CHECK (expected_revision > 0),
    intent_hash text NOT NULL CHECK (intent_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER semantic_batch_reviews_append_only
    BEFORE UPDATE OR DELETE ON semantic_batch_reviews
    FOR EACH ROW EXECUTE FUNCTION reject_semantic_ledger_rewrite();
