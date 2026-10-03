-- X01 allocates lexical term IDs per corpus and pinned analyzer without reusing IDs.
-- New tables are additive and have no backfill; old readers ignore them. Apply before
-- enabling INDEX, rehearse lock/index growth, and replay this checksummed file on failure.
-- Measure allocation p95/p99, contention, and dictionary size; targets remain unmeasured.

CREATE TABLE lexical_dictionary_state (
    corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
    analyzer_id text NOT NULL CHECK (analyzer_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    next_term_id bigint NOT NULL DEFAULT 1 CHECK (next_term_id BETWEEN 1 AND 4294967296),
    PRIMARY KEY (corpus_id, analyzer_id)
);

CREATE TABLE lexical_dictionary_terms (
    corpus_id text NOT NULL,
    analyzer_id text NOT NULL,
    term text NOT NULL CHECK (term <> '' AND octet_length(term) <= 256),
    term_id bigint NOT NULL CHECK (term_id BETWEEN 1 AND 4294967295),
    from_revision bigint NOT NULL CHECK (from_revision > 1),
    PRIMARY KEY (corpus_id, analyzer_id, term),
    UNIQUE (corpus_id, analyzer_id, term_id),
    FOREIGN KEY (corpus_id, analyzer_id)
        REFERENCES lexical_dictionary_state(corpus_id, analyzer_id)
);
CREATE INDEX lexical_dictionary_terms_revision_idx
    ON lexical_dictionary_terms(corpus_id, analyzer_id, from_revision, term_id);

CREATE TABLE lexical_dictionary_operations (
    corpus_id text NOT NULL,
    analyzer_id text NOT NULL,
    operation_key text NOT NULL CHECK (operation_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$'),
    terms_hash text NOT NULL CHECK (terms_hash ~ '^[0-9a-f]{64}$'),
    term_count integer NOT NULL CHECK (term_count BETWEEN 1 AND 10000),
    revision bigint NOT NULL CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (corpus_id, analyzer_id, operation_key),
    FOREIGN KEY (corpus_id, analyzer_id)
        REFERENCES lexical_dictionary_state(corpus_id, analyzer_id)
);

CREATE FUNCTION reject_lexical_dictionary_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'lexical dictionary history is append-only';
END $$;
CREATE TRIGGER lexical_terms_immutable BEFORE UPDATE OR DELETE ON lexical_dictionary_terms
    FOR EACH ROW EXECUTE FUNCTION reject_lexical_dictionary_mutation();
CREATE TRIGGER lexical_operations_immutable BEFORE UPDATE OR DELETE ON lexical_dictionary_operations
    FOR EACH ROW EXECUTE FUNCTION reject_lexical_dictionary_mutation();
