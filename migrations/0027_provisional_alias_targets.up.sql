-- Binds an added alias review to the original provisional creation receipt.
-- Existing review modes remain NULL; immutable receipt history is preserved.
-- One canonical occurrence has exactly one original creation, never an alias
-- chain that could recursively expand review evidence or allocate identities.
CREATE UNIQUE INDEX sourced_alias_provisional_origin
    ON sourced_alias_reviews(corpus_id,canonical_id) WHERE create_provisional;
ALTER TABLE sourced_alias_reviews ADD COLUMN target_origin_operation text;
ALTER TABLE sourced_alias_reviews ADD CONSTRAINT sourced_alias_target_origin_fk
    FOREIGN KEY(corpus_id,target_origin_operation)
    REFERENCES sourced_alias_reviews(corpus_id,operation_key);
ALTER TABLE sourced_alias_reviews ADD CONSTRAINT sourced_alias_target_not_creation
    CHECK (target_origin_operation IS NULL OR NOT create_provisional);
