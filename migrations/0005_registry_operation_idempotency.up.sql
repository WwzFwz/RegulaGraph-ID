-- K01 makes resolution decisions idempotent by operation and records whether the operation
-- created a canonical identity. Existing decisions receive deterministic legacy operation keys.
-- The migration is transactional; dropping the old revision-scoped uniqueness may briefly lock
-- resolution_decisions. Recovery is replaying this unchanged checksummed migration after fixing
-- the cause, never editing an applied file. Benchmark targets remain REQUIRED_UNMEASURED.

ALTER TABLE resolution_decisions
    ADD COLUMN operation_key text,
    ADD COLUMN identity_scope text,
    ADD COLUMN identity_key text,
    ADD COLUMN entity_type smallint,
    ADD COLUMN created_identity boolean NOT NULL DEFAULT false;

UPDATE resolution_decisions AS decision
SET operation_key = 'legacy:' || md5(decision.decision_id),
    identity_scope = identity.identity_scope,
    identity_key = identity.identity_key,
    entity_type = identity.entity_type
FROM canonical_identities AS identity
WHERE decision.corpus_id = identity.corpus_id
  AND decision.canonical_id = identity.canonical_id;

ALTER TABLE resolution_decisions
    ALTER COLUMN operation_key SET NOT NULL,
    ALTER COLUMN identity_scope SET NOT NULL,
    ALTER COLUMN identity_key SET NOT NULL,
    ALTER COLUMN entity_type SET NOT NULL,
    ADD CONSTRAINT resolution_decisions_operation_key_format
        CHECK (operation_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    ADD CONSTRAINT resolution_decisions_entity_type_positive CHECK (entity_type > 0),
    ADD CONSTRAINT resolution_decisions_canonical_fk
        FOREIGN KEY (corpus_id, canonical_id)
        REFERENCES canonical_identities(corpus_id, canonical_id);

DO $$
DECLARE
    old_constraint text;
BEGIN
    SELECT constraint_row.conname INTO old_constraint
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = 'resolution_decisions'::regclass
      AND constraint_row.contype = 'u'
      AND ARRAY(
          SELECT attribute.attname::text
          FROM unnest(constraint_row.conkey) WITH ORDINALITY AS key_column(attnum, position)
          JOIN pg_attribute AS attribute
            ON attribute.attrelid = constraint_row.conrelid
           AND attribute.attnum = key_column.attnum
          ORDER BY key_column.position
      ) = ARRAY['corpus_id','proposal_key','registry_revision']::text[];
    IF old_constraint IS NULL THEN
        RAISE EXCEPTION 'legacy resolution decision uniqueness constraint is missing';
    END IF;
    EXECUTE format('ALTER TABLE resolution_decisions DROP CONSTRAINT %I', old_constraint);
END $$;

ALTER TABLE resolution_decisions
    ADD CONSTRAINT resolution_decisions_operation_proposal_key
        UNIQUE (corpus_id, operation_key, proposal_key);

CREATE INDEX resolution_decisions_operation_idx
    ON resolution_decisions (corpus_id, operation_key, proposal_key);

CREATE TABLE registry_operations (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    operation_key text NOT NULL CHECK (operation_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    claims_hash text NOT NULL CHECK (claims_hash ~ '^[0-9a-f]{64}$'),
    claim_count integer NOT NULL CHECK (claim_count > 0),
    registry_revision bigint NOT NULL CHECK (registry_revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, operation_key)
);

INSERT INTO registry_operations(corpus_id,operation_key,claims_hash,claim_count,registry_revision)
SELECT corpus_id,operation_key,payload_hash,1,registry_revision
FROM resolution_decisions
WHERE operation_key LIKE 'legacy:%';
