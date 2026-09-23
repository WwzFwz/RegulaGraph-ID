-- K01 stores sourced, revision-visible canonical labels and aliases for batched RESOLVE lookup.
-- Existing exact canonical identities are unchanged; no historical label or alias is inferred.
-- New tables and indexes are transactional. A later producer must close old versions and advance
-- lookup_scope_revisions in the same registry transaction before publishing replacements.
-- Measure lookup p95/p99, write contention, and index size; required targets remain unmeasured.

CREATE TABLE registry_entity_profiles (
    corpus_id text NOT NULL,
    canonical_id text NOT NULL,
    from_revision bigint NOT NULL CHECK (from_revision > 0),
    to_revision bigint CHECK (to_revision IS NULL OR to_revision > from_revision),
    entity_type text NOT NULL CHECK (entity_type <> ''),
    canonical_scope text NOT NULL CHECK (canonical_scope <> ''),
    preferred_label text NOT NULL CHECK (preferred_label <> ''),
    review_state smallint NOT NULL CHECK (review_state IN (1, 2)),
    profile_payload bytea NOT NULL CHECK (octet_length(profile_payload) > 0),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, canonical_id, from_revision),
    FOREIGN KEY (corpus_id, canonical_id)
        REFERENCES canonical_identities(corpus_id, canonical_id)
);
CREATE UNIQUE INDEX registry_entity_profiles_current_idx
    ON registry_entity_profiles(corpus_id, canonical_id) WHERE to_revision IS NULL;

CREATE TABLE registry_alias_versions (
    corpus_id text NOT NULL,
    alias_id text NOT NULL,
    from_revision bigint NOT NULL CHECK (from_revision > 0),
    to_revision bigint CHECK (to_revision IS NULL OR to_revision > from_revision),
    canonical_id text NOT NULL,
    entity_type text NOT NULL CHECK (entity_type <> ''),
    canonical_scope text NOT NULL CHECK (canonical_scope <> ''),
    surface text NOT NULL CHECK (surface <> ''),
    normalized_lookup text NOT NULL CHECK (normalized_lookup <> ''),
    language text NOT NULL CHECK (language <> ''),
    support_refs text[] NOT NULL CHECK (cardinality(support_refs) > 0),
    valid_interval bytea,
    alias_payload bytea NOT NULL CHECK (octet_length(alias_payload) > 0),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, alias_id, from_revision),
    FOREIGN KEY (corpus_id, canonical_id)
        REFERENCES canonical_identities(corpus_id, canonical_id)
);
CREATE UNIQUE INDEX registry_alias_versions_current_idx
    ON registry_alias_versions(corpus_id, alias_id) WHERE to_revision IS NULL;
CREATE INDEX registry_alias_versions_lookup_idx
    ON registry_alias_versions(corpus_id, entity_type, canonical_scope, normalized_lookup, alias_id, from_revision, to_revision);

ALTER TABLE lookup_scope_revisions
    ADD COLUMN alias_result_count bigint CHECK (alias_result_count IS NULL OR alias_result_count >= 0);

CREATE TABLE registry_alias_operations (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    operation_key text NOT NULL CHECK (operation_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    registry_revision bigint NOT NULL CHECK (registry_revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, operation_key)
);
