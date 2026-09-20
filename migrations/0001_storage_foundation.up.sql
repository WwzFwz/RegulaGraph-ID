-- S01 PostgreSQL foundation for immutable artifacts, durable jobs, registry revisions,
-- dependency tracking, publication receipts, snapshot visibility, and read leases.
-- Go owns these transactions. Rows for Neo4j/Qdrant are acknowledgements only and do
-- not imply a cross-backend transaction. Measure storage gates through the S01 suite;
-- required numeric targets remain in configs/benchmark-targets.yaml.
-- Status: active forward migration. Apply with checksum tracking; do not edit after a
-- released deployment. Recovery/rollback uses backup and a reviewed follow-up migration.

CREATE TABLE IF NOT EXISTS app_schema_migrations (
    version text PRIMARY KEY,
    checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE corpus_state (
    corpus_id text PRIMARY KEY CHECK (corpus_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    active_snapshot_id text,
    next_snapshot_sequence bigint NOT NULL DEFAULT 1 CHECK (next_snapshot_sequence > 0),
    publisher_fence bigint NOT NULL DEFAULT 0 CHECK (publisher_fence >= 0),
    registry_revision bigint NOT NULL DEFAULT 1 CHECK (registry_revision > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE artifacts (
    artifact_id text PRIMARY KEY CHECK (artifact_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    algorithm text NOT NULL CHECK (algorithm IN ('sha256')),
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    storage_key text NOT NULL UNIQUE,
    media_type text NOT NULL,
    byte_size bigint NOT NULL CHECK (byte_size >= 0),
    schema_version text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (corpus_id, algorithm, digest, byte_size)
);

CREATE TABLE jobs (
    job_id text PRIMARY KEY CHECK (job_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    operation smallint NOT NULL CHECK (operation BETWEEN 1 AND 3),
    state smallint NOT NULL CHECK (state BETWEEN 1 AND 10),
    stage smallint NOT NULL DEFAULT 0 CHECK (stage BETWEEN 0 AND 7),
    input_fingerprint text NOT NULL CHECK (input_fingerprint ~ '^[0-9a-f]{64}$'),
    idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    request_hash text NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    request_payload bytea NOT NULL,
    base_snapshot_id text,
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    lease_owner text,
    lease_fence bigint NOT NULL DEFAULT 0 CHECK (lease_fence >= 0),
    lease_expires_at timestamptz,
    errors jsonb NOT NULL DEFAULT '[]'::jsonb,
    cancellation_requested boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (corpus_id, idempotency_key)
);
CREATE INDEX jobs_claim_idx ON jobs (state, lease_expires_at, created_at);

CREATE TABLE job_checkpoints (
    checkpoint_id text PRIMARY KEY CHECK (checkpoint_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    job_id text NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    stage smallint NOT NULL CHECK (stage BETWEEN 1 AND 7),
    fence bigint NOT NULL CHECK (fence > 0),
    payload bytea NOT NULL,
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (job_id, stage, fence, payload_hash)
);

ALTER TABLE jobs
    ADD COLUMN latest_checkpoint_id text REFERENCES job_checkpoints(checkpoint_id);

CREATE TABLE canonical_identities (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    canonical_id text NOT NULL,
    entity_type smallint NOT NULL CHECK (entity_type > 0),
    identity_scope text NOT NULL,
    identity_key text NOT NULL,
    valid_from_revision bigint NOT NULL CHECK (valid_from_revision > 0),
    valid_to_revision bigint CHECK (valid_to_revision >= valid_from_revision),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, canonical_id),
    UNIQUE (corpus_id, identity_scope, identity_key)
);

CREATE TABLE resolution_decisions (
    decision_id text PRIMARY KEY,
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    proposal_key text NOT NULL,
    canonical_id text NOT NULL,
    registry_revision bigint NOT NULL CHECK (registry_revision > 0),
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    supersedes_decision_id text REFERENCES resolution_decisions(decision_id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (corpus_id, proposal_key, registry_revision)
);

CREATE TABLE lookup_scope_revisions (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    scope_key text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, scope_key)
);

CREATE TABLE artifact_dependencies (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    dependency_kind text NOT NULL CHECK (dependency_kind IN ('artifact', 'fingerprint', 'lookup_scope')),
    dependency_key text NOT NULL,
    dependency_revision bigint,
    producer_manifest_hash text NOT NULL CHECK (producer_manifest_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (artifact_id, dependency_kind, dependency_key)
);
CREATE INDEX artifact_dependencies_reverse_idx
    ON artifact_dependencies (corpus_id, dependency_kind, dependency_key);

CREATE TABLE snapshots (
    snapshot_id text PRIMARY KEY CHECK (snapshot_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    parent_snapshot_id text REFERENCES snapshots(snapshot_id),
    publication_id text NOT NULL UNIQUE,
    job_id text REFERENCES jobs(job_id),
    fence bigint NOT NULL CHECK (fence > 0),
    state smallint NOT NULL CHECK (state BETWEEN 1 AND 7),
    manifest_payload bytea,
    manifest_hash text,
    representation_generation text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    published_at timestamptz,
    UNIQUE (corpus_id, sequence)
);
CREATE UNIQUE INDEX snapshots_one_open_publication
    ON snapshots (corpus_id)
    WHERE state IN (1, 2, 3, 5);

ALTER TABLE corpus_state
    ADD CONSTRAINT corpus_state_active_snapshot_fk
    FOREIGN KEY (active_snapshot_id) REFERENCES snapshots(snapshot_id);

CREATE TABLE publication_backends (
    publication_id text NOT NULL REFERENCES snapshots(publication_id) ON DELETE CASCADE,
    backend smallint NOT NULL CHECK (backend BETWEEN 1 AND 4),
    generation text NOT NULL,
    operations_checksum text NOT NULL CHECK (operations_checksum ~ '^[0-9a-f]{64}$'),
    expected_count bigint NOT NULL CHECK (expected_count >= 0),
    PRIMARY KEY (publication_id, backend)
);

CREATE TABLE backend_receipts (
    publication_id text NOT NULL,
    backend smallint NOT NULL,
    fence bigint NOT NULL CHECK (fence > 0),
    generation text NOT NULL,
    operations_checksum text NOT NULL CHECK (operations_checksum ~ '^[0-9a-f]{64}$'),
    expected_count bigint NOT NULL CHECK (expected_count >= 0),
    accepted_count bigint NOT NULL CHECK (accepted_count >= 0),
    rejected_count bigint NOT NULL CHECK (rejected_count >= 0),
    durable_ack boolean NOT NULL,
    search_ready boolean NOT NULL,
    payload bytea NOT NULL,
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (publication_id, backend),
    FOREIGN KEY (publication_id, backend)
        REFERENCES publication_backends(publication_id, backend) ON DELETE CASCADE
);

CREATE TABLE publication_operations (
    operation_key text PRIMARY KEY,
    publication_id text NOT NULL REFERENCES snapshots(publication_id) ON DELETE CASCADE,
    backend smallint NOT NULL CHECK (backend BETWEEN 1 AND 4),
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    before_image_artifact_id text REFERENCES artifacts(artifact_id),
    fence bigint NOT NULL CHECK (fence > 0),
    status text NOT NULL CHECK (status IN ('planned', 'applied', 'compensated')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (publication_id, backend, operation_key)
);

CREATE TABLE outbox_events (
    event_id text PRIMARY KEY,
    topic text NOT NULL,
    aggregate_id text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    delivered_at timestamptz
);
CREATE INDEX outbox_pending_idx ON outbox_events (created_at) WHERE delivered_at IS NULL;

CREATE TABLE snapshot_read_leases (
    lease_id text PRIMARY KEY,
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    snapshot_id text NOT NULL REFERENCES snapshots(snapshot_id),
    owner_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (expires_at > created_at)
);
CREATE INDEX snapshot_read_leases_gc_idx ON snapshot_read_leases (snapshot_id, expires_at);
