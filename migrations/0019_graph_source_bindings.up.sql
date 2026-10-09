-- Stores immutable graph metadata-transform receipts after published CHUNK
-- membership, original RESOLVE checkpoint and target fence are checked together.
-- No backfill or changes to historical artifacts. Apply before graph source binding.
-- Aborted targets retain receipts for audit; GC/retirement is a separate policy.
CREATE TABLE graph_source_bindings (
    publication_id text NOT NULL REFERENCES snapshots(publication_id),
    source_job_id text NOT NULL REFERENCES jobs(job_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    fence bigint NOT NULL CHECK(fence > 0),
    source_checkpoint_id text NOT NULL REFERENCES job_checkpoints(checkpoint_id),
    base_publication_id text NOT NULL REFERENCES index_source_snapshots(publication_id),
    original_extraction_id text NOT NULL REFERENCES artifacts(artifact_id),
    original_resolution_id text NOT NULL REFERENCES artifacts(artifact_id),
    bound_extraction_id text NOT NULL REFERENCES artifacts(artifact_id),
    bound_resolution_id text NOT NULL REFERENCES artifacts(artifact_id),
    payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 65536),
    payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY(publication_id,source_job_id)
);
CREATE TRIGGER graph_source_binding_immutable BEFORE UPDATE OR DELETE ON graph_source_bindings
    FOR EACH ROW EXECUTE FUNCTION reject_index_job_inventory_mutation();
