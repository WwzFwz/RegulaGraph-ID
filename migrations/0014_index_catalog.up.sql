-- Immutable generation/route and collision-checked point catalog for X01.
-- One physical collection belongs to one publication. Old/aborted namespaces
-- are retained; this migration does not activate query routes or delete data.
-- Apply transactionally before the writer. No backfill; rehearse lock/index
-- growth on production-scale data before release (benchmark not measured).
CREATE TABLE index_generations (
 corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
 generation_id text NOT NULL,
 publication_id text NOT NULL UNIQUE REFERENCES snapshots(publication_id),
 fence bigint NOT NULL CHECK(fence > 0),
 endpoint text NOT NULL,
 collection_name text NOT NULL,
 generation_payload bytea NOT NULL,
 PRIMARY KEY(corpus_id,generation_id),
 UNIQUE(endpoint,collection_name)
);
CREATE TABLE index_points (
 corpus_id text NOT NULL,
 generation_id text NOT NULL,
 record_id text NOT NULL,
 point_id uuid NOT NULL,
 identity_digest text NOT NULL CHECK(identity_digest ~ '^[0-9a-f]{64}$'),
 record_payload bytea NOT NULL,
 PRIMARY KEY(corpus_id,generation_id,record_id),
 UNIQUE(point_id),
 FOREIGN KEY(corpus_id,generation_id) REFERENCES index_generations(corpus_id,generation_id)
);
CREATE FUNCTION reject_index_catalog_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'index generation and point identity are immutable';
END $$;
CREATE TRIGGER index_generations_immutable BEFORE UPDATE OR DELETE ON index_generations
 FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
CREATE TRIGGER index_points_immutable BEFORE UPDATE OR DELETE ON index_points
 FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
