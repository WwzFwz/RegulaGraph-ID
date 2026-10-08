-- Records publication-owned metadata envelopes around immutable CHUNK artifacts.
-- No source/checkpoint bytes are rewritten or backfilled. One snapshot/scope is
-- frozen per publication and one original/derived pair per source job. Apply
-- before the source binder; retirement/GC is intentionally not implemented here.
CREATE TABLE index_source_snapshots (
    publication_id text PRIMARY KEY REFERENCES snapshots(publication_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    fence bigint NOT NULL CHECK(fence > 0),
    snapshot_payload bytea NOT NULL,
    snapshot_hash text NOT NULL CHECK(snapshot_hash ~ '^[0-9a-f]{64}$'),
    auth_scope text NOT NULL CHECK(auth_scope <> '')
);
CREATE TABLE index_source_bindings (
    publication_id text NOT NULL REFERENCES index_source_snapshots(publication_id),
    source_job_id text NOT NULL REFERENCES jobs(job_id),
    original_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    bound_artifact_id text NOT NULL UNIQUE REFERENCES artifacts(artifact_id),
    original_reference bytea NOT NULL,
    bound_reference bytea NOT NULL,
    PRIMARY KEY(publication_id, source_job_id)
);
CREATE TRIGGER index_source_snapshot_immutable BEFORE UPDATE OR DELETE ON index_source_snapshots
    FOR EACH ROW EXECUTE FUNCTION reject_index_job_inventory_mutation();
CREATE TRIGGER index_source_binding_immutable BEFORE UPDATE OR DELETE ON index_source_bindings
    FOR EACH ROW EXECUTE FUNCTION reject_index_job_inventory_mutation();
