// Supports source-occurrence creation in the sourced alias transaction. The
// registry revision is read without inventing a target identity. Every policy
// lookup scope must be empty under the corpus lock; incomplete lookup metadata
// fails closed. An empty exact lookup does not prove semantic uniqueness.
// No model or file IO is performed under locks. Measure lock wait/p95 and false
// splits against benchmark-targets.yaml; acceptance remains unmeasured.
package postgres

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) LoadAliasRegistryRevision(ctx context.Context, corpus string, revision uint64) (uint64, error) {
	if !storageIDPattern.MatchString(corpus) || revision > math.MaxInt64 {
		return 0, errors.New("invalid registry selection")
	}
	var current, floor int64
	if err := r.pool.QueryRow(ctx, `SELECT registry_revision,registry_history_floor FROM corpus_state WHERE corpus_id=$1`, corpus).Scan(&current, &floor); err != nil {
		return 0, err
	}
	if current <= 0 || floor <= 0 || floor > current {
		return 0, domain.ErrPersistentIntegrity
	}
	if revision == 0 {
		revision = uint64(current)
	}
	if revision < uint64(floor) || revision > uint64(current) {
		return 0, ErrConflict
	}
	return revision, nil
}

func verifyProvisionalEmptyScopes(ctx context.Context, tx pgx.Tx, corpus string, scopes []domain.RegistryLookupScope) error {
	if len(scopes) == 0 || len(scopes) > domain.DefaultWireLimits.MaxItems {
		return errors.New("complete bounded lookup scopes required")
	}
	types, names, surfaces, keys := []string{}, []string{}, []string{}, []string{}
	for _, s := range scopes {
		types = append(types, s.EntityType)
		names = append(names, s.CanonicalScope)
		surfaces = append(surfaces, s.NormalizedLookup)
		keys = append(keys, registryLookupScopeID(s.EntityType, s.CanonicalScope, s.NormalizedLookup))
	}
	var occupied bool
	err := tx.QueryRow(ctx, `WITH input AS (SELECT * FROM unnest($2::text[],$3::text[],$4::text[],$5::text[]) AS x(kind,scope,surface,key))
 SELECT EXISTS(SELECT 1 FROM input JOIN registry_alias_versions a ON a.corpus_id=$1 AND a.entity_type=input.kind
 AND a.canonical_scope=input.scope AND a.normalized_lookup=input.surface AND a.to_revision IS NULL)
 OR EXISTS(SELECT 1 FROM input JOIN lookup_scope_revisions l ON l.corpus_id=$1 AND l.scope_key=input.key
 WHERE l.alias_result_count IS NULL OR l.alias_result_count<>0)`, corpus, types, names, surfaces, keys).Scan(&occupied)
	if err != nil {
		return err
	}
	if occupied {
		return errors.New("provisional creation requires empty complete lookups in every policy scope; review existing candidates")
	}
	return nil
}
