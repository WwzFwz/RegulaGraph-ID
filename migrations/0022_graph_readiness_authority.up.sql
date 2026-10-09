-- Records the mutable authority observed when a sealed graph is acknowledged.
-- Receipt bytes remain immutable; a fresh source admission may refresh this
-- revision/job fingerprint after recovery. Activation checks it under the same
-- corpus/job locks as the active-pointer CAS. No existing receipt is backfilled.
-- Apply before graph receipt writers; rehearse production upgrade separately.
CREATE TABLE graph_readiness_authority (
    publication_id text PRIMARY KEY REFERENCES graph_generations(publication_id),
    catalog_hash text NOT NULL CHECK(catalog_hash ~ '^[0-9a-f]{64}$'),
    registry_revision bigint NOT NULL CHECK(registry_revision>0),
    history_floor bigint NOT NULL CHECK(history_floor>0 AND history_floor<=registry_revision),
    jobs_hash text NOT NULL CHECK(jobs_hash ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
