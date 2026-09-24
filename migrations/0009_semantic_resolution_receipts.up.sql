-- K01 persists semantic LINK/DEFER decisions separately from the exact-key allocator ledger.
-- One operation advances the corpus registry revision once; replay is immutable and keyed by
-- the normalized request hash. PostgreSQL FK/uniqueness protect local atomicity only: the Go
-- adapter must verify candidate artifact provenance and CAS before inserting these rows.
-- Forward-only migration, no legacy backfill. A failed transaction rolls back unchanged;
-- recover by replaying this checksummed file after fixing the cause. Rehearse index/lock time
-- on a production-size copy before rollout; required latency targets remain unmeasured.

CREATE TABLE registry_semantic_operations (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    operation_key text NOT NULL CHECK (operation_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    request_hash text NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    expected_revision bigint NOT NULL CHECK (expected_revision > 0),
    committed_revision bigint NOT NULL CHECK (committed_revision = expected_revision + 1),
    decision_count integer NOT NULL CHECK (decision_count > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, operation_key),
    UNIQUE (corpus_id, operation_key, committed_revision)
);

CREATE TABLE registry_semantic_reviews (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    review_id text NOT NULL CHECK (review_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    source_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    candidate_artifact_id text NOT NULL REFERENCES artifacts(artifact_id),
    candidate_hash text NOT NULL CHECK (candidate_hash ~ '^[0-9a-f]{64}$'),
    proposal_id text NOT NULL CHECK (proposal_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    proposal_hash text NOT NULL CHECK (proposal_hash ~ '^[0-9a-f]{64}$'),
    canonical_id text NOT NULL,
    actor text NOT NULL CHECK (actor ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    reason text NOT NULL CHECK (reason <> ''),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, review_id),
    FOREIGN KEY (corpus_id, canonical_id)
        REFERENCES canonical_identities(corpus_id, canonical_id)
);

CREATE FUNCTION reject_semantic_review_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'semantic reviews are immutable';
    END IF;
    IF ROW(NEW.corpus_id, NEW.review_id, NEW.source_artifact_id,
           NEW.candidate_artifact_id, NEW.candidate_hash, NEW.proposal_id, NEW.proposal_hash,
           NEW.canonical_id, NEW.actor, NEW.reason, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.corpus_id, OLD.review_id, OLD.source_artifact_id,
           OLD.candidate_artifact_id, OLD.candidate_hash, OLD.proposal_id, OLD.proposal_hash,
           OLD.canonical_id, OLD.actor, OLD.reason, OLD.created_at)
       OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL THEN
        RAISE EXCEPTION 'semantic review content cannot be rewritten';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER registry_semantic_reviews_immutable
    BEFORE UPDATE OR DELETE ON registry_semantic_reviews
    FOR EACH ROW EXECUTE FUNCTION reject_semantic_review_rewrite();

CREATE TABLE registry_semantic_decisions (
    corpus_id text NOT NULL,
    operation_key text NOT NULL,
    proposal_id text NOT NULL CHECK (proposal_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    correlation_id text NOT NULL CHECK (correlation_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    decision_id text NOT NULL CHECK (decision_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    action smallint NOT NULL CHECK (action IN (2, 5)),
    canonical_id text,
    review_id text,
    registry_revision bigint NOT NULL CHECK (registry_revision > 0),
    decision_payload bytea NOT NULL CHECK (octet_length(decision_payload) > 0),
    decision_hash text NOT NULL CHECK (decision_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, operation_key, proposal_id),
    UNIQUE (corpus_id, proposal_id),
    UNIQUE (corpus_id, decision_id),
    FOREIGN KEY (corpus_id, operation_key)
        REFERENCES registry_semantic_operations(corpus_id, operation_key),
    FOREIGN KEY (corpus_id, operation_key, registry_revision)
        REFERENCES registry_semantic_operations(corpus_id, operation_key, committed_revision),
    FOREIGN KEY (corpus_id, canonical_id)
        REFERENCES canonical_identities(corpus_id, canonical_id),
    FOREIGN KEY (corpus_id, review_id)
        REFERENCES registry_semantic_reviews(corpus_id, review_id),
    CHECK ((action = 2 AND canonical_id IS NOT NULL AND review_id IS NOT NULL) OR
           (action = 5 AND canonical_id IS NULL AND review_id IS NULL))
);

CREATE INDEX registry_semantic_decisions_proposal_idx
    ON registry_semantic_decisions(corpus_id, proposal_id, registry_revision);

CREATE FUNCTION reject_semantic_ledger_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'semantic registry ledger is append-only';
END $$;

CREATE TRIGGER registry_semantic_operations_append_only
    BEFORE UPDATE OR DELETE ON registry_semantic_operations
    FOR EACH ROW EXECUTE FUNCTION reject_semantic_ledger_rewrite();

CREATE TRIGGER registry_semantic_decisions_append_only
    BEFORE UPDATE OR DELETE ON registry_semantic_decisions
    FOR EACH ROW EXECUTE FUNCTION reject_semantic_ledger_rewrite();
