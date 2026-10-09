-- Freezes a physical Neo4j namespace and complete output inventory before writes.
-- Catalog insertion and operation intent are one PostgreSQL transaction; neither
-- is a backend-ready receipt. No backfill or deletion of prior generations.
-- Apply before graph writer startup. Failed migration replays identical checksum;
-- rehearse lock/index growth at corpus scale before production acceptance.
CREATE TABLE graph_generations (
    publication_id text PRIMARY KEY REFERENCES graph_job_inventories(publication_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    generation_id text NOT NULL,
    fence bigint NOT NULL CHECK(fence>0),
    endpoint text NOT NULL,
    database_name text NOT NULL,
    payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 16777216),
    payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
    UNIQUE(corpus_id,generation_id),
    UNIQUE(endpoint,database_name,corpus_id,generation_id)
);
CREATE TRIGGER graph_generations_immutable BEFORE UPDATE OR DELETE ON graph_generations
    FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
