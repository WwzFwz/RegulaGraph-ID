-- Retains whether a lookup dependency observed an empty result at its recorded revision.
-- This flag is required by incremental invalidation: a later registry insertion must invalidate
-- an artifact even when the original lookup had revision zero and produced no dependency rows.
-- The migration runner owns the surrounding transaction; do not add BEGIN/COMMIT here.
ALTER TABLE artifact_dependencies
    ADD COLUMN empty_result boolean NOT NULL DEFAULT false,
    ADD COLUMN dependency_fingerprint text;

ALTER TABLE artifact_dependencies
    ADD CONSTRAINT artifact_dependencies_empty_result_scope
    CHECK (NOT empty_result OR dependency_kind = 'lookup_scope'),
    ADD CONSTRAINT artifact_dependencies_fingerprint_format
    CHECK (dependency_fingerprint IS NULL OR dependency_fingerprint ~ '^[0-9a-f]{64}$');
