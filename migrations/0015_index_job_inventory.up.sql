-- Persists one immutable initial INDEX inventory and its per-plan child jobs.
-- Apply before the INDEX scheduler. Existing jobs/plans are not backfilled or
-- reclassified. Inventory creation joins job creation in one transaction; worker
-- checkpoints and publication retain their existing fencing/state ownership.
CREATE TABLE index_job_inventories (
    publication_id text PRIMARY KEY REFERENCES snapshots(publication_id),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    fence bigint NOT NULL CHECK (fence > 0),
    auth_scope text NOT NULL CHECK (auth_scope <> '' AND octet_length(auth_scope) <= 256),
    snapshot_payload bytea NOT NULL,
    inventory_hash text NOT NULL CHECK (inventory_hash ~ '^[0-9a-f]{64}$'),
    job_count integer NOT NULL CHECK (job_count BETWEEN 1 AND 256)
);
CREATE TABLE index_job_assignments (
    job_id text PRIMARY KEY REFERENCES jobs(job_id),
    publication_id text NOT NULL REFERENCES index_job_inventories(publication_id),
    source_job_id text NOT NULL REFERENCES jobs(job_id),
    ordinal integer NOT NULL CHECK (ordinal BETWEEN 0 AND 255),
    plan_payload bytea NOT NULL,
    plan_reference bytea NOT NULL,
    UNIQUE (publication_id, ordinal)
);
CREATE FUNCTION reject_index_job_inventory_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'INDEX inventory and assignments are immutable';
END $$;
CREATE TRIGGER index_inventory_immutable BEFORE UPDATE OR DELETE ON index_job_inventories
    FOR EACH ROW EXECUTE FUNCTION reject_index_job_inventory_mutation();
CREATE TRIGGER index_assignment_immutable BEFORE UPDATE OR DELETE ON index_job_assignments
    FOR EACH ROW EXECUTE FUNCTION reject_index_job_inventory_mutation();
