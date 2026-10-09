-- Freezes one ASSEMBLE inventory with its source-bound child jobs atomically.
-- No historical backfill; apply before using GraphJobAdmission.Schedule.
-- Append-only inventory survives aborted publications for audit. Recovery retries
-- exact hashes; GC/retirement requires an explicit separate policy.
CREATE TABLE graph_job_inventories (
    publication_id text PRIMARY KEY REFERENCES snapshots(publication_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    fence bigint NOT NULL CHECK(fence > 0),
    payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 67108864),
    payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
    job_count integer NOT NULL CHECK(job_count BETWEEN 1 AND 256)
);
CREATE TABLE graph_job_assignments (
    job_id text PRIMARY KEY REFERENCES jobs(job_id),
    publication_id text NOT NULL REFERENCES graph_job_inventories(publication_id),
    source_job_id text NOT NULL,
    plan_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 255),
    UNIQUE(publication_id, source_job_id),
    UNIQUE(publication_id, ordinal),
    FOREIGN KEY(publication_id,source_job_id) REFERENCES graph_source_bindings(publication_id,source_job_id)
);
CREATE TRIGGER graph_inventory_immutable BEFORE UPDATE OR DELETE ON graph_job_inventories
    FOR EACH ROW EXECUTE FUNCTION reject_index_job_inventory_mutation();
CREATE TRIGGER graph_assignment_immutable BEFORE UPDATE OR DELETE ON graph_job_assignments
    FOR EACH ROW EXECUTE FUNCTION reject_index_job_inventory_mutation();
