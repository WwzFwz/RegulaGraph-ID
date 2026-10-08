-- Retains alias lookup observations and immutable publication registry bindings.
-- Existing corpora can only serve history from their revision at migration time;
-- earlier counts are deliberately not reconstructed from current mutable rows.
-- Upgrade registry writers together with this migration. Tables grow with changed
-- scopes/publications; rehearse migration locks and growth before production use.
ALTER TABLE corpus_state ADD COLUMN registry_history_floor bigint NOT NULL DEFAULT 1
 CHECK (registry_history_floor > 0);
UPDATE corpus_state SET registry_history_floor=registry_revision;

CREATE TABLE registry_lookup_history (
 corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
 scope_key text NOT NULL,
 revision bigint NOT NULL CHECK(revision > 0),
 alias_result_count bigint NOT NULL CHECK(alias_result_count >= 0),
 PRIMARY KEY(corpus_id,scope_key,revision)
);
INSERT INTO registry_lookup_history(corpus_id,scope_key,revision,alias_result_count)
 SELECT corpus_id,scope_key,revision,alias_result_count FROM lookup_scope_revisions
 WHERE alias_result_count IS NOT NULL;
CREATE TABLE snapshot_registry_bindings (
 publication_id text PRIMARY KEY REFERENCES snapshots(publication_id),
 corpus_id text NOT NULL REFERENCES corpus_state(corpus_id),
 fence bigint NOT NULL CHECK(fence > 0),
 registry_revision bigint NOT NULL CHECK(registry_revision > 0)
);
CREATE TRIGGER registry_lookup_history_immutable BEFORE UPDATE OR DELETE ON registry_lookup_history
 FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
CREATE TRIGGER snapshot_registry_bindings_immutable BEFORE UPDATE OR DELETE ON snapshot_registry_bindings
 FOR EACH ROW EXECUTE FUNCTION reject_index_catalog_mutation();
