-- Distinguishes source-occurrence provisional creation from existing BIND alias
-- review. Defaults preserve old receipts; no entity or alias is backfilled.
-- Both modes retain the immutable review trigger and atomic operation foreign key.
ALTER TABLE sourced_alias_reviews ADD COLUMN create_provisional boolean NOT NULL DEFAULT false;
ALTER TABLE sourced_alias_reviews ADD CONSTRAINT provisional_review_advances_revision
    CHECK (NOT create_provisional OR registry_revision = expected_revision + 1);
