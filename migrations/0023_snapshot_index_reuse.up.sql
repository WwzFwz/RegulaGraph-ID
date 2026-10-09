-- Binds a graph snapshot to an unchanged physical index and original source
-- snapshot after full backend readback. Mapping and target receipt commit together.
-- No backfill or mutation of old generations; existing snapshots keep their route.
-- Apply before reuse publication/readers; production upgrade rehearsal required.
CREATE TABLE snapshot_index_reuse (
    publication_id text PRIMARY KEY REFERENCES graph_generations(publication_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    source_publication_id text NOT NULL REFERENCES snapshots(publication_id),
    payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 16777216),
    payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$')
);
CREATE TRIGGER snapshot_index_reuse_immutable BEFORE UPDATE OR DELETE ON snapshot_index_reuse
    FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
