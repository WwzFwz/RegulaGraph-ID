// Reads revision-pinned alias candidates for bounded lookup scopes in one PostgreSQL snapshot
// and one batched query. Immutable payload hashes and indexed columns are checked on every read;
// ambiguous matches and empty-scope revisions remain explicit. The caller owns legal scope and
// normalization policy. Measure p95/p99, scanned rows, pool wait, and candidate coverage on gold;
// targets in configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// RegistryLookupScope keeps the existing PostgreSQL API while the shared key lives in domain.
type RegistryLookupScope = domain.RegistryLookupScope

// RegistryLookupResult retains every alias and the revision of its exact positive/negative key.
type RegistryLookupResult struct {
	Scope      RegistryLookupScope
	Revision   *pb.LookupScopeRevision
	Candidates []*pb.CanonicalEntity
	Aliases    []*pb.Alias
}

// LookupCanonicalAliases returns results in input order at one corpus registry revision. A caller
// must bind this revision and all scope observations to the immutable RESOLVE candidate batch.
func (r *Repository) LookupCanonicalAliases(ctx context.Context, corpusID string,
	scopes []RegistryLookupScope, maximumScopes, maximumAliasesPerScope int) ([]RegistryLookupResult, uint64, error) {
	if !storageIDPattern.MatchString(corpusID) || maximumScopes <= 0 || maximumAliasesPerScope <= 0 ||
		len(scopes) == 0 || len(scopes) > maximumScopes || maximumAliasesPerScope >= math.MaxInt32 ||
		len(scopes) > 100_000/maximumAliasesPerScope {
		return nil, 0, errors.New("bounded registry lookup scopes are required")
	}
	types := make([]string, len(scopes))
	canonicalScopes := make([]string, len(scopes))
	normalized := make([]string, len(scopes))
	scopeIDs := make([]string, len(scopes))
	seen := make(map[string]bool, len(scopes))
	for i, scope := range scopes {
		if aliasEntityType(scope.EntityType) == 0 || !validRegistryLookupText(scope.CanonicalScope) ||
			!validRegistryLookupText(scope.NormalizedLookup) {
			return nil, 0, fmt.Errorf("invalid registry lookup scope at index %d", i)
		}
		scopeID := registryLookupScopeID(scope.EntityType, scope.CanonicalScope, scope.NormalizedLookup)
		if seen[scopeID] {
			return nil, 0, errors.New("duplicate registry lookup scope")
		}
		seen[scopeID] = true
		types[i], canonicalScopes[i], normalized[i], scopeIDs[i] =
			scope.EntityType, scope.CanonicalScope, scope.NormalizedLookup, scopeID
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("begin registry lookup: %w", err)
	}
	defer tx.Rollback(ctx)
	var current int64
	if err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1`, corpusID).Scan(&current); err != nil {
		return nil, 0, fmt.Errorf("read registry revision: %w", err)
	}
	if current <= 0 {
		return nil, 0, fmt.Errorf("invalid registry revision: %w", domain.ErrPersistentIntegrity)
	}
	rows, err := tx.Query(ctx, `WITH input AS (
		SELECT entity_type,canonical_scope,normalized_lookup,scope_id,ordinal
		FROM unnest($2::text[],$3::text[],$4::text[],$5::text[])
		WITH ORDINALITY AS item(entity_type,canonical_scope,normalized_lookup,scope_id,ordinal)
	)
	SELECT input.ordinal,COALESCE(revision.revision,0),revision.alias_result_count,
		match.alias_id,match.canonical_id,match.surface,match.language,match.support_refs,
		match.valid_interval,match.alias_scope,match.alias_type,match.alias_normalized,
		match.alias_from,match.alias_payload,match.alias_hash,
		match.profile_type,match.profile_scope,match.preferred_label,match.review_state,
		match.profile_revision,match.profile_payload,match.profile_hash,
		match.identity_type,match.identity_scope,match.identity_key,match.identity_from,match.identity_to
	FROM input
	LEFT JOIN lookup_scope_revisions AS revision
		ON revision.corpus_id=$1 AND revision.scope_key=input.scope_id
	LEFT JOIN LATERAL (
		SELECT alias.alias_id,alias.canonical_id,alias.surface,alias.language,
			alias.support_refs,alias.valid_interval,alias.canonical_scope AS alias_scope,
			alias.entity_type AS alias_type,alias.normalized_lookup AS alias_normalized,
			alias.from_revision AS alias_from,alias.alias_payload,alias.record_hash AS alias_hash,
			profile.entity_type AS profile_type,profile.canonical_scope AS profile_scope,
			profile.preferred_label,profile.review_state,profile.from_revision AS profile_revision,
			profile.profile_payload,profile.record_hash AS profile_hash,
			identity.entity_type AS identity_type,identity.identity_scope,identity.identity_key,
			identity.valid_from_revision AS identity_from,identity.valid_to_revision AS identity_to
		FROM registry_alias_versions AS alias
		LEFT JOIN registry_entity_profiles AS profile
			ON profile.corpus_id=alias.corpus_id AND profile.canonical_id=alias.canonical_id
			AND profile.from_revision <= $6
			AND (profile.to_revision IS NULL OR profile.to_revision > $6)
		LEFT JOIN canonical_identities AS identity
			ON identity.corpus_id=alias.corpus_id AND identity.canonical_id=alias.canonical_id
		WHERE alias.corpus_id=$1 AND alias.entity_type=input.entity_type
			AND alias.canonical_scope=input.canonical_scope
			AND alias.normalized_lookup=input.normalized_lookup
			AND alias.from_revision <= $6
			AND (alias.to_revision IS NULL OR alias.to_revision > $6)
		ORDER BY alias.alias_id
		LIMIT $7
	) AS match ON true
	ORDER BY input.ordinal,match.alias_id`, corpusID, types, canonicalScopes, normalized, scopeIDs,
		current, maximumAliasesPerScope+1)
	if err != nil {
		return nil, 0, fmt.Errorf("batch registry alias lookup: %w", err)
	}
	defer rows.Close()
	results := make([]RegistryLookupResult, len(scopes))
	for i, scope := range scopes {
		results[i] = RegistryLookupResult{Scope: scope, Revision: &pb.LookupScopeRevision{ScopeId: scopeIDs[i]}}
	}
	seenCandidates := make([]map[string]bool, len(scopes))
	seenAliases := make([]map[string]bool, len(scopes))
	expectedCounts := make([]*int64, len(scopes))
	bytesRead := 0
	for rows.Next() {
		var row registryAliasReadRow
		if err = row.scan(rows); err != nil {
			return nil, 0, fmt.Errorf("scan registry alias: %w", err)
		}
		if row.ordinal < 1 || row.ordinal > int64(len(results)) || row.revision < 0 || row.revision > current ||
			row.revision > 0 && row.resultCount == nil || row.resultCount != nil && *row.resultCount < 0 {
			return nil, 0, fmt.Errorf("registry lookup ordinal, revision, or count is invalid: %w", domain.ErrPersistentIntegrity)
		}
		if row.resultCount != nil && *row.resultCount > int64(maximumAliasesPerScope) {
			return nil, 0, fmt.Errorf("registry alias count exceeds result cap: %w", ErrResultLimit)
		}
		index := int(row.ordinal - 1)
		results[index].Revision.Revision = uint64(row.revision)
		expectedCounts[index] = row.resultCount
		if row.aliasID == nil {
			continue
		}
		if len(results[index].Aliases) >= maximumAliasesPerScope {
			return nil, 0, fmt.Errorf("registry alias result limit exceeded: %w", ErrResultLimit)
		}
		bytesRead += len(row.aliasRaw) + len(row.profileRaw)
		if bytesRead > domain.DefaultWireLimits.MaxBytes {
			return nil, 0, errors.New("registry alias payload byte limit exceeded")
		}
		entity, alias, integrityErr := row.materialize(corpusID, scopes[index], current)
		if integrityErr != nil {
			return nil, 0, integrityErr
		}
		if seenAliases[index] == nil {
			seenAliases[index] = map[string]bool{}
		}
		if seenAliases[index][alias.Meta.RecordId] {
			return nil, 0, fmt.Errorf("duplicate visible alias version: %w", domain.ErrPersistentIntegrity)
		}
		seenAliases[index][alias.Meta.RecordId] = true
		results[index].Aliases = append(results[index].Aliases, alias)
		if seenCandidates[index] == nil {
			seenCandidates[index] = map[string]bool{}
		}
		if !seenCandidates[index][entity.Meta.RecordId] {
			seenCandidates[index][entity.Meta.RecordId] = true
			results[index].Candidates = append(results[index].Candidates, entity)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range results {
		if expectedCounts[i] != nil && int64(len(results[i].Aliases)) != *expectedCounts[i] {
			return nil, 0, fmt.Errorf("registry alias result count differs from scope revision: %w", domain.ErrPersistentIntegrity)
		}
		results[i].Revision.EmptyResult = len(results[i].Candidates) == 0
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("commit registry read snapshot: %w", err)
	}
	return results, uint64(current), nil
}

type registryAliasReadRow struct {
	ordinal, revision                                                               int64
	resultCount                                                                     *int64
	aliasID, canonicalID, surface, language, aliasScope, aliasType, aliasNormalized *string
	aliasFrom                                                                       *int64
	aliasRaw                                                                        []byte
	aliasHash                                                                       *string
	profileType, profileScope, preferredLabel                                       *string
	reviewState                                                                     *int16
	profileRevision                                                                 *int64
	profileRaw                                                                      []byte
	profileHash                                                                     *string
	identityType                                                                    *int16
	identityScope, identityKey                                                      *string
	identityFrom, identityTo                                                        *int64
	supportRefs                                                                     []string
	intervalRaw                                                                     []byte
}

func (row registryAliasReadRow) aliasIDValue() string {
	if row.aliasID == nil {
		return ""
	}
	return *row.aliasID
}

func (row *registryAliasReadRow) scan(rows pgx.Rows) error {
	return rows.Scan(&row.ordinal, &row.revision, &row.resultCount,
		&row.aliasID, &row.canonicalID, &row.surface, &row.language, &row.supportRefs,
		&row.intervalRaw, &row.aliasScope, &row.aliasType, &row.aliasNormalized,
		&row.aliasFrom, &row.aliasRaw, &row.aliasHash,
		&row.profileType, &row.profileScope, &row.preferredLabel, &row.reviewState,
		&row.profileRevision, &row.profileRaw, &row.profileHash,
		&row.identityType, &row.identityScope, &row.identityKey, &row.identityFrom, &row.identityTo)
}

func (row registryAliasReadRow) materialize(corpusID string, scope RegistryLookupScope,
	current int64) (*pb.CanonicalEntity, *pb.Alias, error) {
	if row.revision == 0 || row.canonicalID == nil || row.surface == nil || row.language == nil ||
		row.aliasScope == nil || row.aliasType == nil || row.aliasNormalized == nil || row.aliasFrom == nil ||
		row.aliasHash == nil || len(row.aliasRaw) == 0 || row.profileType == nil ||
		row.profileScope == nil || row.preferredLabel == nil || row.reviewState == nil ||
		row.profileRevision == nil || row.profileHash == nil || len(row.profileRaw) == 0 ||
		row.identityType == nil || row.identityScope == nil || row.identityKey == nil || row.identityFrom == nil ||
		*row.aliasFrom <= 0 || *row.aliasFrom > row.revision ||
		*row.profileRevision <= 0 || *row.profileRevision > current || *row.identityFrom > current ||
		row.identityTo != nil && *row.identityTo <= current ||
		*row.aliasScope != scope.CanonicalScope || *row.aliasType != scope.EntityType ||
		*row.aliasNormalized != scope.NormalizedLookup ||
		*row.profileType != scope.EntityType || *row.profileScope != scope.CanonicalScope ||
		*row.identityType != aliasEntityType(*row.profileType) ||
		*row.reviewState != int16(pb.ReviewState_REVIEW_STATE_UNREVIEWED) &&
			*row.reviewState != int16(pb.ReviewState_REVIEW_STATE_APPROVED) {
		return nil, nil, fmt.Errorf("registry alias/profile columns are invalid: %w", domain.ErrPersistentIntegrity)
	}
	entity := &pb.CanonicalEntity{}
	if err := decodeRegistryPayload(row.profileRaw, *row.profileHash, entity); err != nil {
		return nil, nil, err
	}
	if entity.GetMeta().GetRecordId() != *row.canonicalID || entity.GetMeta().GetCorpusId() != corpusID ||
		entity.EntityType != *row.profileType || entity.Scope != *row.profileScope ||
		entity.PreferredLabel != *row.preferredLabel || int16(entity.ReviewState) != *row.reviewState ||
		!profileContainsExactIdentity(entity, aliasIdentity{identityScope: *row.identityScope, identityKey: *row.identityKey}) {
		return nil, nil, fmt.Errorf("canonical profile payload differs from registry columns: %w", domain.ErrPersistentIntegrity)
	}
	entity.RegistryRevision = uint64(*row.profileRevision)
	if err := domain.ValidateWire(entity, domain.DefaultWireLimits); err != nil {
		return nil, nil, fmt.Errorf("invalid stored canonical profile: %w", domain.ErrPersistentIntegrity)
	}
	alias := &pb.Alias{}
	if err := decodeRegistryPayload(row.aliasRaw, *row.aliasHash, alias); err != nil {
		return nil, nil, err
	}
	var encodedInterval []byte
	if alias.ValidInterval != nil {
		var err error
		encodedInterval, err = (proto.MarshalOptions{Deterministic: true}).Marshal(alias.ValidInterval)
		if err != nil {
			return nil, nil, err
		}
	}
	if alias.GetMeta().GetRecordId() != *row.aliasID || alias.GetMeta().GetCorpusId() != corpusID ||
		alias.CanonicalId != *row.canonicalID || alias.Surface != *row.surface ||
		alias.NormalizedLookup != *row.aliasNormalized || alias.Language != *row.language ||
		alias.Scope != *row.aliasScope || !slices.Equal(alias.SupportRefs, row.supportRefs) ||
		!bytes.Equal(encodedInterval, row.intervalRaw) {
		return nil, nil, fmt.Errorf("alias payload differs from registry columns: %w", domain.ErrPersistentIntegrity)
	}
	if err := domain.ValidateWire(alias, domain.DefaultWireLimits); err != nil {
		return nil, nil, fmt.Errorf("invalid stored alias: %w", domain.ErrPersistentIntegrity)
	}
	return entity, alias, nil
}

func decodeRegistryPayload(raw []byte, digest string, message proto.Message) error {
	if len(raw) == 0 || len(raw) > domain.DefaultWireLimits.MaxBytes {
		return fmt.Errorf("registry record payload size is invalid: %w", domain.ErrPersistentIntegrity)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return fmt.Errorf("registry record payload hash mismatch: %w", domain.ErrPersistentIntegrity)
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false, RecursionLimit: domain.DefaultWireLimits.MaxDepth}).Unmarshal(raw, message); err != nil {
		return fmt.Errorf("decode registry record payload: %w", domain.ErrPersistentIntegrity)
	}
	return nil
}

func validRegistryLookupText(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsRune(value, '\x00') && utf8.ValidString(value)
}
