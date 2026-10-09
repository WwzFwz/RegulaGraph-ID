-- Operator approval for an existing canonical identity and an exact EXTRACT alias.
-- Written atomically with profile/alias/lookup revision and operation receipt.
-- Immutable review hashes preserve source, policy and expected-revision binding;
-- approval is not a legal identity verification or graph publication marker.
CREATE TABLE sourced_alias_reviews (
    corpus_id text NOT NULL,
    operation_key text NOT NULL,
    plan_hash text NOT NULL CHECK(plan_hash ~ '^[0-9a-f]{64}$'),
    expected_revision bigint NOT NULL CHECK(expected_revision > 0),
    registry_revision bigint NOT NULL CHECK(registry_revision >= expected_revision),
    actor text NOT NULL CHECK(octet_length(actor) BETWEEN 1 AND 256),
    reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 1024),
    source_artifact_id text NOT NULL,
    target_artifact_id text NOT NULL,
    policy_hash text NOT NULL CHECK(policy_hash ~ '^[0-9a-f]{64}$'),
    mention_id text NOT NULL,
    canonical_id text NOT NULL,
    alias_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(corpus_id,operation_key),
    FOREIGN KEY(corpus_id,operation_key) REFERENCES registry_alias_operations(corpus_id,operation_key)
);
CREATE TRIGGER sourced_alias_reviews_immutable BEFORE UPDATE OR DELETE ON sourced_alias_reviews
    FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
