// Registers sourced aliases against existing canonical identities in one revisioned transaction.
// Go owns revision CAS and operation replay; every alias keeps support references and a stable
// lookup-scope revision so RESOLVE can invalidate prior empty results. This append-only entry
// point does not decide whether two legal entities are equivalent or rewrite prior decisions.
// Measure batch p95/p99, lock wait, index growth, and false merge/split on gold; numeric targets
// in configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"sort"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// AliasRegistration pairs a canonical registry record with one sourced alias. The canonical
// identity must already exist; this operation never allocates an identity from text similarity.
type AliasRegistration struct {
	Entity *pb.CanonicalEntity
	Alias  *pb.Alias
}

type checkedAliasRegistration struct {
	entity      *pb.CanonicalEntity
	alias       *pb.Alias
	entityRaw   []byte
	aliasRaw    []byte
	entityHash  string
	aliasHash   string
	intervalRaw []byte
}

// RegisterCanonicalAliases inserts immutable profile/alias versions and advances every affected
// lookup scope together with the corpus revision. Replaying an operation with changed content
// fails; an existing alias/profile ID cannot silently change its meaning.
func (r *Repository) RegisterCanonicalAliases(ctx context.Context, corpusID, operationKey string,
	expectedRevision uint64, registrations []AliasRegistration) (uint64, error) {
	checked, payloadHash, err := checkAliasRegistrations(corpusID, operationKey, expectedRevision, registrations)
	if err != nil {
		return 0, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, fmt.Errorf("begin alias registration: %w", err)
	}
	defer tx.Rollback(ctx)
	var current int64
	if err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, corpusID).Scan(&current); err != nil {
		return 0, fmt.Errorf("lock registry revision: %w", err)
	}
	var storedHash string
	var storedRevision int64
	err = tx.QueryRow(ctx, `SELECT payload_hash,registry_revision FROM registry_alias_operations
		WHERE corpus_id=$1 AND operation_key=$2`, corpusID, operationKey).Scan(&storedHash, &storedRevision)
	if err == nil {
		if storedHash != payloadHash || storedRevision > current || storedRevision <= 0 {
			return 0, fmt.Errorf("alias operation replay differs: %w", ErrConflict)
		}
		if err = verifyAliasOperationReplay(ctx, tx, corpusID, storedRevision, checked); err != nil {
			return 0, err
		}
		return uint64(storedRevision), nil
	}
	if err != pgx.ErrNoRows {
		return 0, fmt.Errorf("inspect alias operation: %w", err)
	}
	if expectedRevision != uint64(current) {
		return 0, fmt.Errorf("registry revision changed: %w", ErrConflict)
	}

	canonicalIDs := make([]string, 0, len(checked))
	aliasIDs := make([]string, 0, len(checked))
	seenCanonical := map[string]bool{}
	for _, item := range checked {
		id := item.entity.Meta.RecordId
		if !seenCanonical[id] {
			canonicalIDs = append(canonicalIDs, id)
			seenCanonical[id] = true
		}
		aliasIDs = append(aliasIDs, item.alias.Meta.RecordId)
	}
	identities, err := readAliasCanonicalIdentities(ctx, tx, corpusID, canonicalIDs)
	if err != nil {
		return 0, err
	}
	for _, item := range checked {
		identity, ok := identities[item.entity.Meta.RecordId]
		if !ok || identity.fromRevision > int64(item.entity.RegistryRevision) ||
			identity.fromRevision > current || identity.toRevision != nil && *identity.toRevision <= current ||
			identity.entityType != aliasEntityType(item.entity.EntityType) ||
			!profileContainsExactIdentity(item.entity, identity) {
			return 0, fmt.Errorf("alias canonical identity is absent, closed, or wrong-type: %w", ErrConflict)
		}
	}
	profiles, err := readCurrentProfiles(ctx, tx, corpusID, canonicalIDs, identities, current)
	if err != nil {
		return 0, err
	}
	aliases, err := readRecordHashes(ctx, tx, `SELECT alias_id,record_hash FROM registry_alias_versions
		WHERE corpus_id=$1 AND alias_id=ANY($2) AND to_revision IS NULL`, corpusID, aliasIDs)
	if err != nil {
		return 0, err
	}
	newProfiles := map[string]checkedAliasRegistration{}
	newAliases := make([]checkedAliasRegistration, 0, len(checked))
	existingAliases := make([]checkedAliasRegistration, 0, len(checked))
	for _, item := range checked {
		storedProfile, profileExists := profiles[item.entity.Meta.RecordId]
		if profileExists {
			if storedProfile != item.entityHash {
				return 0, fmt.Errorf("canonical profile changed without a review decision: %w", ErrConflict)
			}
		} else {
			newProfiles[item.entity.Meta.RecordId] = item
		}
		if stored, exists := aliases[item.alias.Meta.RecordId]; exists {
			if !profileExists {
				return 0, fmt.Errorf("stored alias has no canonical profile: %w", domain.ErrPersistentIntegrity)
			}
			if stored != item.aliasHash {
				return 0, fmt.Errorf("alias ID changed without a review decision: %w", ErrConflict)
			}
			existingAliases = append(existingAliases, item)
		} else {
			newAliases = append(newAliases, item)
		}
	}
	if len(existingAliases) > 0 {
		if err = verifyAliasOperationReplay(ctx, tx, corpusID, current, existingAliases); err != nil {
			return 0, err
		}
	}
	newRevision := current
	if len(newProfiles) != 0 || len(newAliases) != 0 {
		if current == math.MaxInt64 {
			return 0, errors.New("registry revision exhausted")
		}
		newRevision++
		if _, err = tx.Exec(ctx, `UPDATE corpus_state SET registry_revision=$2,updated_at=clock_timestamp()
			WHERE corpus_id=$1`, corpusID, newRevision); err != nil {
			return 0, fmt.Errorf("advance registry revision: %w", err)
		}
		profileIDs := make([]string, 0, len(newProfiles))
		for id := range newProfiles {
			profileIDs = append(profileIDs, id)
		}
		sort.Strings(profileIDs)
		for _, id := range profileIDs {
			item := newProfiles[id]
			if _, err = tx.Exec(ctx, `INSERT INTO registry_entity_profiles
				(corpus_id,canonical_id,from_revision,entity_type,canonical_scope,preferred_label,
				review_state,profile_payload,record_hash)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, corpusID, id, newRevision, item.entity.EntityType,
				item.entity.Scope, item.entity.PreferredLabel, int16(item.entity.ReviewState),
				item.entityRaw, item.entityHash); err != nil {
				return 0, fmt.Errorf("insert canonical profile: %w", err)
			}
		}
		for _, item := range newAliases {
			a := item.alias
			if _, err = tx.Exec(ctx, `INSERT INTO registry_alias_versions
				(corpus_id,alias_id,from_revision,canonical_id,entity_type,canonical_scope,surface,
				normalized_lookup,language,support_refs,valid_interval,alias_payload,record_hash)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, corpusID, a.Meta.RecordId,
				newRevision, a.CanonicalId, item.entity.EntityType, a.Scope, a.Surface, a.NormalizedLookup,
				a.Language, a.SupportRefs, item.intervalRaw, item.aliasRaw, item.aliasHash); err != nil {
				return 0, fmt.Errorf("insert registry alias: %w", err)
			}
		}
		if len(newAliases) > 0 {
			if err = advanceAliasLookupScopes(ctx, tx, corpusID, newRevision, newAliases); err != nil {
				return 0, err
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO registry_alias_operations(corpus_id,operation_key,payload_hash,registry_revision)
		VALUES ($1,$2,$3,$4)`, corpusID, operationKey, payloadHash, newRevision); err != nil {
		return 0, fmt.Errorf("record alias operation: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit alias registration: %w", err)
	}
	return uint64(newRevision), nil
}

type aliasIdentity struct {
	entityType    int16
	identityScope string
	identityKey   string
	fromRevision  int64
	toRevision    *int64
}

func readAliasCanonicalIdentities(ctx context.Context, tx pgx.Tx, corpusID string, ids []string) (map[string]aliasIdentity, error) {
	rows, err := tx.Query(ctx, `SELECT canonical_id,entity_type,identity_scope,identity_key,valid_from_revision,valid_to_revision
		FROM canonical_identities WHERE corpus_id=$1 AND canonical_id=ANY($2)`, corpusID, ids)
	if err != nil {
		return nil, fmt.Errorf("read alias canonical identities: %w", err)
	}
	defer rows.Close()
	result := make(map[string]aliasIdentity, len(ids))
	for rows.Next() {
		var id string
		var value aliasIdentity
		if err = rows.Scan(&id, &value.entityType, &value.identityScope, &value.identityKey,
			&value.fromRevision, &value.toRevision); err != nil {
			return nil, err
		}
		result[id] = value
	}
	return result, rows.Err()
}

func readCurrentProfiles(ctx context.Context, tx pgx.Tx, corpusID string, ids []string,
	identities map[string]aliasIdentity, current int64) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT canonical_id,entity_type,canonical_scope,preferred_label,
		review_state,from_revision,profile_payload,record_hash
		FROM registry_entity_profiles WHERE corpus_id=$1 AND canonical_id=ANY($2)
		AND to_revision IS NULL`, corpusID, ids)
	if err != nil {
		return nil, fmt.Errorf("read current canonical profiles: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string, len(ids))
	for rows.Next() {
		var id, entityType, scope, label, digest string
		var reviewState int16
		var revision int64
		var raw []byte
		if err = rows.Scan(&id, &entityType, &scope, &label, &reviewState, &revision, &raw, &digest); err != nil {
			return nil, err
		}
		identity, exists := identities[id]
		if _, duplicate := result[id]; duplicate || !exists || revision <= 0 || revision > current {
			return nil, fmt.Errorf("current canonical profile identity is invalid: %w", domain.ErrPersistentIntegrity)
		}
		entity := &pb.CanonicalEntity{}
		if err = decodeRegistryPayload(raw, digest, entity); err != nil {
			return nil, err
		}
		if entity.GetMeta().GetRecordId() != id || entity.GetMeta().GetCorpusId() != corpusID ||
			entity.EntityType != entityType || entity.Scope != scope ||
			entity.PreferredLabel != label || int16(entity.ReviewState) != reviewState ||
			!profileContainsExactIdentity(entity, identity) {
			return nil, fmt.Errorf("current canonical profile differs from payload: %w", domain.ErrPersistentIntegrity)
		}
		entity.RegistryRevision = uint64(revision)
		if err = domain.ValidateWire(entity, domain.DefaultWireLimits); err != nil {
			return nil, fmt.Errorf("invalid current canonical profile: %w", domain.ErrPersistentIntegrity)
		}
		result[id] = digest
	}
	return result, rows.Err()
}

func profileContainsExactIdentity(entity *pb.CanonicalEntity, identity aliasIdentity) bool {
	for _, key := range entity.IdentityKeys {
		if key.GetNamespace() == identity.identityScope && key.GetValue() == identity.identityKey {
			return true
		}
	}
	return false
}

func readRecordHashes(ctx context.Context, tx pgx.Tx, query string, args ...any) (map[string]string, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var id, digest string
		if err = rows.Scan(&id, &digest); err != nil {
			return nil, err
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fmt.Errorf("overlapping registry record versions: %w", domain.ErrPersistentIntegrity)
		}
		result[id] = digest
	}
	return result, rows.Err()
}

func verifyAliasOperationReplay(ctx context.Context, tx pgx.Tx, corpusID string, revision int64,
	checked []checkedAliasRegistration) error {
	aliasIDs := make([]string, 0, len(checked))
	expected := make(map[string]checkedAliasRegistration, len(checked))
	for _, item := range checked {
		id := item.alias.Meta.RecordId
		aliasIDs = append(aliasIDs, id)
		expected[id] = item
	}
	rows, err := tx.Query(ctx, `SELECT 1::bigint,$3::bigint,NULL::bigint,
		alias.alias_id,alias.canonical_id,alias.surface,alias.language,alias.support_refs,
		alias.valid_interval,alias.canonical_scope,alias.entity_type,alias.normalized_lookup,
		alias.from_revision,alias.alias_payload,alias.record_hash,
		profile.entity_type,profile.canonical_scope,profile.preferred_label,profile.review_state,
		profile.from_revision,profile.profile_payload,profile.record_hash,
		identity.entity_type,identity.identity_scope,identity.identity_key,
		identity.valid_from_revision,identity.valid_to_revision
		FROM registry_alias_versions AS alias
		LEFT JOIN registry_entity_profiles AS profile
			ON profile.corpus_id=alias.corpus_id AND profile.canonical_id=alias.canonical_id
			AND profile.from_revision <= $3 AND (profile.to_revision IS NULL OR profile.to_revision > $3)
		LEFT JOIN canonical_identities AS identity
			ON identity.corpus_id=alias.corpus_id AND identity.canonical_id=alias.canonical_id
		WHERE alias.corpus_id=$1 AND alias.alias_id=ANY($2)
			AND alias.from_revision <= $3 AND (alias.to_revision IS NULL OR alias.to_revision > $3)`,
		corpusID, aliasIDs, revision)
	if err != nil {
		return fmt.Errorf("inspect alias replay rows: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var row registryAliasReadRow
		if err = row.scan(rows); err != nil {
			return fmt.Errorf("scan alias replay row: %w", err)
		}
		item, ok := expected[row.aliasIDValue()]
		if !ok || seen[row.aliasIDValue()] || row.aliasHash == nil || row.profileHash == nil ||
			*row.aliasHash != item.aliasHash || *row.profileHash != item.entityHash {
			return fmt.Errorf("alias operation rows differ from durable header: %w", domain.ErrPersistentIntegrity)
		}
		scope := RegistryLookupScope{EntityType: item.entity.EntityType,
			CanonicalScope: item.alias.Scope, NormalizedLookup: item.alias.NormalizedLookup}
		if _, _, err = row.materialize(corpusID, scope, revision); err != nil {
			return err
		}
		seen[row.aliasIDValue()] = true
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(seen) != len(checked) {
		return fmt.Errorf("alias operation rows are missing: %w", domain.ErrPersistentIntegrity)
	}
	return nil
}

func aliasEntityType(name string) int16 {
	switch name {
	case "regulation":
		return domain.CanonicalEntityTypeRegulation
	case "organization":
		return domain.CanonicalEntityTypeOrganization
	default:
		return 0
	}
}

func checkAliasRegistrations(corpusID, operationKey string, expectedRevision uint64,
	registrations []AliasRegistration) ([]checkedAliasRegistration, string, error) {
	if !storageIDPattern.MatchString(corpusID) || !storageIDPattern.MatchString(operationKey) ||
		expectedRevision == 0 || expectedRevision >= math.MaxInt64 ||
		len(registrations) == 0 || len(registrations) > maximumRegistryClaims {
		return nil, "", errors.New("bounded alias registration and expected revision are required")
	}
	for _, entry := range registrations {
		if entry.Entity == nil || entry.Alias == nil || entry.Alias.Meta == nil {
			return nil, "", errors.New("alias registration requires canonical and alias records")
		}
	}
	ordered := append([]AliasRegistration(nil), registrations...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Alias.GetMeta().GetRecordId() < ordered[j].Alias.GetMeta().GetRecordId()
	})
	checked := make([]checkedAliasRegistration, 0, len(ordered))
	entityHashes := map[string]string{}
	seenAliases := map[string]bool{}
	work := 0
	bytes := 0
	operationHash := sha256.New()
	for _, entry := range ordered {
		entity, alias := entry.Entity, entry.Alias
		if entity == nil || alias == nil || entity.Meta == nil || alias.Meta == nil ||
			entity.Meta.CorpusId != corpusID || alias.Meta.CorpusId != corpusID ||
			entity.Meta.RecordId == "" || alias.Meta.RecordId == "" ||
			entity.Meta.SchemaVersion != alias.Meta.SchemaVersion || entity.Meta.SchemaVersion == 0 ||
			alias.CanonicalId != entity.Meta.RecordId || alias.Scope != entity.Scope ||
			!validRegistryLookupText(entity.Scope) || !validRegistryLookupText(alias.NormalizedLookup) ||
			aliasEntityType(entity.EntityType) == 0 || entity.RegistryRevision == 0 ||
			entity.RegistryRevision > expectedRevision ||
			entity.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED ||
			seenAliases[alias.Meta.RecordId] {
			return nil, "", errors.New("alias registration identity, type, scope, or revision is invalid")
		}
		seenAliases[alias.Meta.RecordId] = true
		work += 2 + len(entity.IdentityKeys) + len(alias.SupportRefs)
		bytes += proto.Size(entity) + proto.Size(alias)
		if work > 100_000 || bytes > domain.DefaultWireLimits.MaxBytes {
			return nil, "", errors.New("alias registration reference budget exceeded")
		}
		seenSupport := map[string]bool{}
		for _, support := range alias.SupportRefs {
			if seenSupport[support] {
				return nil, "", errors.New("duplicate alias support reference")
			}
			seenSupport[support] = true
		}
		if err := domain.ValidateWire(entity, domain.DefaultWireLimits); err != nil {
			return nil, "", fmt.Errorf("invalid canonical profile: %w", err)
		}
		if err := domain.ValidateWire(alias, domain.DefaultWireLimits); err != nil {
			return nil, "", fmt.Errorf("invalid alias: %w", err)
		}
		canonical := proto.Clone(entity).(*pb.CanonicalEntity)
		canonical.RegistryRevision = 0
		entityRaw, entityHash, err := marshalRegistryRecord(canonical)
		if err != nil {
			return nil, "", err
		}
		if previous, exists := entityHashes[entity.Meta.RecordId]; exists && previous != entityHash {
			return nil, "", errors.New("canonical profile differs within one operation")
		}
		entityHashes[entity.Meta.RecordId] = entityHash
		aliasRaw, aliasHash, err := marshalRegistryRecord(alias)
		if err != nil {
			return nil, "", err
		}
		var intervalRaw []byte
		if alias.ValidInterval != nil {
			intervalRaw, err = (proto.MarshalOptions{Deterministic: true}).Marshal(alias.ValidInterval)
			if err != nil {
				return nil, "", err
			}
		}
		writeRegistryHashPart(operationHash, []byte(entity.Meta.RecordId))
		writeRegistryHashPart(operationHash, []byte(entityHash))
		writeRegistryHashPart(operationHash, []byte(alias.Meta.RecordId))
		writeRegistryHashPart(operationHash, []byte(aliasHash))
		checked = append(checked, checkedAliasRegistration{
			entity: entity, alias: alias, entityRaw: entityRaw, aliasRaw: aliasRaw,
			entityHash: entityHash, aliasHash: aliasHash, intervalRaw: intervalRaw,
		})
	}
	return checked, hex.EncodeToString(operationHash.Sum(nil)), nil
}

func advanceAliasLookupScopes(ctx context.Context, tx pgx.Tx, corpusID string, revision int64,
	aliases []checkedAliasRegistration) error {
	types := make([]string, len(aliases))
	scopes := make([]string, len(aliases))
	normalized := make([]string, len(aliases))
	scopeIDs := make([]string, len(aliases))
	for i, item := range aliases {
		types[i] = item.entity.EntityType
		scopes[i] = item.alias.Scope
		normalized[i] = item.alias.NormalizedLookup
		scopeIDs[i] = registryLookupScopeID(types[i], scopes[i], normalized[i])
	}
	_, err := tx.Exec(ctx, `WITH input AS (
		SELECT DISTINCT entity_type,canonical_scope,normalized_lookup,scope_id
		FROM unnest($2::text[],$3::text[],$4::text[],$5::text[])
		AS key(entity_type,canonical_scope,normalized_lookup,scope_id)
	), counts AS (
		SELECT input.scope_id,COUNT(alias.alias_id) AS result_count
		FROM input LEFT JOIN registry_alias_versions AS alias
			ON alias.corpus_id=$1 AND alias.entity_type=input.entity_type
			AND alias.canonical_scope=input.canonical_scope
			AND alias.normalized_lookup=input.normalized_lookup
			AND alias.from_revision <= $6
			AND (alias.to_revision IS NULL OR alias.to_revision > $6)
		GROUP BY input.scope_id
	)
	INSERT INTO lookup_scope_revisions(corpus_id,scope_key,revision,alias_result_count)
	SELECT $1,scope_id,$6,result_count FROM counts
	ON CONFLICT (corpus_id,scope_key) DO UPDATE SET
		revision=EXCLUDED.revision,alias_result_count=EXCLUDED.alias_result_count,
		updated_at=clock_timestamp()`, corpusID, types, scopes, normalized, scopeIDs, revision)
	if err != nil {
		return fmt.Errorf("advance alias lookup scopes: %w", err)
	}
	return nil
}

func hashRegistryRecord(message proto.Message) (string, error) {
	_, digest, err := marshalRegistryRecord(message)
	return digest, err
}

func marshalRegistryRecord(message proto.Message) ([]byte, string, error) {
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func writeRegistryHashPart(target hash.Hash, raw []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(raw)))
	target.Write(size[:])
	target.Write(raw)
}

func registryLookupScopeID(entityType, scope, normalized string) string {
	sum := sha256.New()
	for _, field := range []string{"registry-alias-lookup:v1", entityType, scope, normalized} {
		writeRegistryHashPart(sum, []byte(field))
	}
	return "lookup:" + hex.EncodeToString(sum.Sum(nil))
}
