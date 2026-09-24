-- Indexes source mentions from successful EXTRACT checkpoints for batched candidate hydration.
-- The Go coordinator verifies artifact bytes before this catalog is committed in the same
-- fenced transaction as the checkpoint. Source rows are immutable; no semantic truth or
-- canonical assignment is inferred. Older checkpoints are not backfilled without revalidation.
-- Additive rollout requires this migration before the new coordinator; measure index size,
-- lock time and lookup p95/p99 under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
CREATE TABLE extraction_evidence_sources (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    mention_id text NOT NULL CHECK (mention_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    checkpoint_id text NOT NULL REFERENCES job_checkpoints(checkpoint_id),
    snapshot_key text NOT NULL CHECK (snapshot_key ~ '^[0-9a-f]{64}$'),
    auth_scope_ref text NOT NULL,
    PRIMARY KEY (corpus_id, mention_id, artifact_id)
);
CREATE INDEX extraction_evidence_lookup_idx
    ON extraction_evidence_sources (corpus_id, snapshot_key, auth_scope_ref, mention_id);
CREATE TRIGGER extraction_evidence_sources_append_only
    BEFORE UPDATE OR DELETE ON extraction_evidence_sources
    FOR EACH ROW EXECUTE FUNCTION reject_semantic_ledger_rewrite();
