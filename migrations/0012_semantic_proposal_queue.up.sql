-- Stores advisory RESOLVE output pointers atomically with WAITING_REVIEW admission.
-- Source checkpoint and registered artifacts remain immutable; this row grants no LINK
-- authority. Measure transaction/lock p95/p99 and queue age under benchmark-targets.yaml;
-- acceptance remains REQUIRED_UNMEASURED. No historic jobs are synthesized.
CREATE TABLE semantic_proposal_queue (
    job_id text PRIMARY KEY REFERENCES jobs(job_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    source_checkpoint_id text NOT NULL REFERENCES job_checkpoints(checkpoint_id),
    source_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    candidate_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    input_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    output_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (source_artifact_id <> candidate_artifact_id AND candidate_artifact_id <> input_artifact_id
        AND input_artifact_id <> output_artifact_id AND source_artifact_id <> input_artifact_id
        AND source_artifact_id <> output_artifact_id AND candidate_artifact_id <> output_artifact_id)
);
CREATE INDEX semantic_proposal_queue_corpus ON semantic_proposal_queue(corpus_id, created_at, job_id);
CREATE TRIGGER semantic_proposal_queue_append_only BEFORE UPDATE OR DELETE ON semantic_proposal_queue
    FOR EACH ROW EXECUTE FUNCTION reject_semantic_ledger_rewrite();
