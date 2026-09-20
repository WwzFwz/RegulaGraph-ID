// Resolves exact identity claims through the revisioned PostgreSQL canonical registry.
//
// The operation locks one corpus revision, replays identical operation keys without mutation,
// allocates opaque canonical IDs for unseen exact keys, and stores decision history atomically.
// It performs no fuzzy matching, semantic merge, or legal-metadata inference. Benchmark registry
// batch p50/p95/p99, contention, and false merge/split gates from configs/benchmark-targets.yaml;
// all numeric targets remain REQUIRED_UNMEASURED.
package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"regulagraph.local/server/internal/domain"
)

const maximumRegistryClaims = 10_000

type registryResolvedIdentity struct {
	canonicalID string
	entityType  int16
	created     bool
}

// ResolveCanonicalIdentities atomically resolves a bounded exact-key batch. An operation key can
// be retried only with byte-equivalent claims; reuse with a changed payload returns ErrConflict.
func (r *Repository) ResolveCanonicalIdentities(
	ctx context.Context,
	corpusID string,
	operationKey string,
	expectedRevision uint64,
	claims []domain.CanonicalIdentityClaim,
) ([]domain.CanonicalIdentityAssignment, uint64, error) {
	ordered, err := validateRegistryClaims(corpusID, operationKey, expectedRevision, claims)
	if err != nil {
		return nil, 0, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, 0, fmt.Errorf("begin registry resolution: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO corpus_state(corpus_id) VALUES ($1) ON CONFLICT DO NOTHING`, corpusID); err != nil {
		return nil, 0, fmt.Errorf("ensure registry corpus: %w", err)
	}
	var current int64
	if err = tx.QueryRow(ctx, `SELECT registry_revision FROM corpus_state WHERE corpus_id=$1 FOR UPDATE`, corpusID).Scan(&current); err != nil {
		return nil, 0, fmt.Errorf("lock registry revision: %w", err)
	}

	claimsHash, err := registryClaimsHash(ordered)
	if err != nil {
		return nil, 0, err
	}
	replayed, replayRevision, found, err := loadRegistryOperation(ctx, tx, corpusID, operationKey, claimsHash, ordered)
	if err != nil {
		return nil, 0, err
	}
	if found {
		if replayRevision > uint64(current) {
			return nil, 0, fmt.Errorf("registry operation is ahead of corpus revision: %w", domain.ErrPersistentIntegrity)
		}
		return replayed, replayRevision, nil
	}
	if expectedRevision != 0 && expectedRevision != uint64(current) {
		return nil, 0, fmt.Errorf("registry revision changed: %w", ErrConflict)
	}

	byIdentity := make(map[string]registryResolvedIdentity, len(ordered))
	newKeys := make([]string, 0)
	for _, claim := range ordered {
		lookup := claim.IdentityScope + "\x00" + claim.IdentityKey
		if resolved, exists := byIdentity[lookup]; exists {
			if resolved.entityType != claim.EntityType {
				return nil, 0, fmt.Errorf("identity key reused across entity types: %w", ErrConflict)
			}
			continue
		}
		var canonicalID string
		var entityType int16
		var validFrom int64
		var validTo *int64
		scanErr := tx.QueryRow(ctx, `SELECT canonical_id,entity_type,valid_from_revision,valid_to_revision
			FROM canonical_identities WHERE corpus_id=$1 AND identity_scope=$2 AND identity_key=$3`,
			corpusID, claim.IdentityScope, claim.IdentityKey).Scan(&canonicalID, &entityType, &validFrom, &validTo)
		if scanErr == pgx.ErrNoRows {
			byIdentity[lookup] = registryResolvedIdentity{entityType: claim.EntityType, created: true}
			newKeys = append(newKeys, lookup)
			continue
		}
		if scanErr != nil {
			return nil, 0, fmt.Errorf("lookup canonical identity: %w", scanErr)
		}
		if validFrom > current {
			return nil, 0, fmt.Errorf("canonical identity starts after the corpus revision: %w", domain.ErrPersistentIntegrity)
		}
		if validTo != nil || entityType != claim.EntityType {
			return nil, 0, fmt.Errorf("canonical identity is closed or has another type: %w", ErrConflict)
		}
		byIdentity[lookup] = registryResolvedIdentity{canonicalID: canonicalID, entityType: entityType}
	}

	revision := uint64(current)
	if len(newKeys) > 0 {
		if current == math.MaxInt64 {
			return nil, 0, errors.New("registry revision exhausted")
		}
		current++
		revision = uint64(current)
		if _, err = tx.Exec(ctx, `UPDATE corpus_state SET registry_revision=$2,updated_at=clock_timestamp() WHERE corpus_id=$1`, corpusID, current); err != nil {
			return nil, 0, fmt.Errorf("advance registry revision: %w", err)
		}
		sort.Strings(newKeys)
		for _, lookup := range newKeys {
			resolved := byIdentity[lookup]
			separator := strings.IndexByte(lookup, 0)
			canonicalID, idErr := allocateCanonicalID("canonical:entity:")
			if idErr != nil {
				return nil, 0, idErr
			}
			if _, err = tx.Exec(ctx, `INSERT INTO canonical_identities(
				corpus_id,canonical_id,entity_type,identity_scope,identity_key,valid_from_revision)
				VALUES ($1,$2,$3,$4,$5,$6)`, corpusID, canonicalID, resolved.entityType,
				lookup[:separator], lookup[separator+1:], current); err != nil {
				return nil, 0, fmt.Errorf("insert canonical identity: %w", err)
			}
			resolved.canonicalID = canonicalID
			byIdentity[lookup] = resolved
		}
	}

	assignments := make([]domain.CanonicalIdentityAssignment, 0, len(ordered))
	for _, claim := range ordered {
		resolved := byIdentity[claim.IdentityScope+"\x00"+claim.IdentityKey]
		decisionID := registryDecisionID(corpusID, operationKey, claim)
		if _, err = tx.Exec(ctx, `INSERT INTO resolution_decisions(
			decision_id,corpus_id,operation_key,proposal_key,canonical_id,registry_revision,payload_hash,
			created_identity,identity_scope,identity_key,entity_type)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, decisionID, corpusID, operationKey, claim.ProposalKey,
			resolved.canonicalID, int64(revision), claim.PayloadHash, resolved.created,
			claim.IdentityScope, claim.IdentityKey, claim.EntityType); err != nil {
			return nil, 0, fmt.Errorf("record resolution decision: %w", err)
		}
		assignments = append(assignments, domain.CanonicalIdentityAssignment{
			ProposalKey: claim.ProposalKey, CanonicalID: resolved.canonicalID,
			Revision: revision, Created: resolved.created,
		})
	}
	if _, err = tx.Exec(ctx, `INSERT INTO registry_operations(
		corpus_id,operation_key,claims_hash,claim_count,registry_revision) VALUES ($1,$2,$3,$4,$5)`,
		corpusID, operationKey, claimsHash, len(ordered), int64(revision)); err != nil {
		return nil, 0, fmt.Errorf("record registry operation: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("commit registry resolution: %w", err)
	}
	return assignments, revision, nil
}

func validateRegistryClaims(corpusID, operationKey string, expectedRevision uint64, claims []domain.CanonicalIdentityClaim) ([]domain.CanonicalIdentityClaim, error) {
	if !storageIDPattern.MatchString(corpusID) || !storageIDPattern.MatchString(operationKey) ||
		expectedRevision > math.MaxInt64 || len(claims) == 0 || len(claims) > maximumRegistryClaims {
		return nil, errors.New("valid bounded registry operation is required")
	}
	ordered := append([]domain.CanonicalIdentityClaim(nil), claims...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ProposalKey < ordered[j].ProposalKey })
	for index, claim := range ordered {
		if !storageIDPattern.MatchString(claim.ProposalKey) || claim.EntityType <= 0 ||
			claim.IdentityScope == "" || len(claim.IdentityScope) > 255 ||
			!sha256Pattern.MatchString(claim.IdentityKey) || !sha256Pattern.MatchString(claim.PayloadHash) {
			return nil, errors.New("invalid canonical identity claim")
		}
		if index > 0 && claim.ProposalKey == ordered[index-1].ProposalKey {
			return nil, errors.New("duplicate registry proposal key")
		}
	}
	return ordered, nil
}

func loadRegistryOperation(ctx context.Context, tx pgx.Tx, corpusID, operationKey, claimsHash string, claims []domain.CanonicalIdentityClaim) ([]domain.CanonicalIdentityAssignment, uint64, bool, error) {
	var storedClaimsHash string
	var storedCount int
	var storedRevision int64
	err := tx.QueryRow(ctx, `SELECT claims_hash,claim_count,registry_revision FROM registry_operations
		WHERE corpus_id=$1 AND operation_key=$2`, corpusID, operationKey).
		Scan(&storedClaimsHash, &storedCount, &storedRevision)
	if err == pgx.ErrNoRows {
		var decisionsExist bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM resolution_decisions WHERE corpus_id=$1 AND operation_key=$2)`, corpusID, operationKey).Scan(&decisionsExist); err != nil {
			return nil, 0, false, fmt.Errorf("inspect orphan registry decisions: %w", err)
		}
		if decisionsExist {
			return nil, 0, false, fmt.Errorf("registry operation header is missing: %w", domain.ErrPersistentIntegrity)
		}
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("load registry operation header: %w", err)
	}
	if storedRevision <= 0 || storedCount != len(claims) || storedClaimsHash != claimsHash {
		return nil, 0, false, fmt.Errorf("operation key reused with different claims: %w", ErrConflict)
	}

	rows, err := tx.Query(ctx, `SELECT decision.proposal_key,decision.canonical_id,decision.registry_revision,
		decision.payload_hash,decision.created_identity,decision.identity_scope,decision.identity_key,
		decision.entity_type,identity.identity_scope,identity.identity_key,identity.entity_type,
		identity.valid_from_revision,identity.valid_to_revision
		FROM resolution_decisions AS decision
		JOIN canonical_identities AS identity
		  ON identity.corpus_id=decision.corpus_id AND identity.canonical_id=decision.canonical_id
		WHERE decision.corpus_id=$1 AND decision.operation_key=$2 ORDER BY decision.proposal_key`, corpusID, operationKey)
	if err != nil {
		return nil, 0, false, fmt.Errorf("load registry operation: %w", err)
	}
	defer rows.Close()
	assignments := make([]domain.CanonicalIdentityAssignment, 0, len(claims))
	payloads := make([]string, 0, len(claims))
	revision := uint64(storedRevision)
	for rows.Next() {
		var assignment domain.CanonicalIdentityAssignment
		var decisionRevision, validFrom int64
		var validTo *int64
		var payload, decisionScope, decisionKey, identityScope, identityKey string
		var decisionType, identityType int16
		if err = rows.Scan(&assignment.ProposalKey, &assignment.CanonicalID, &decisionRevision, &payload,
			&assignment.Created, &decisionScope, &decisionKey, &decisionType, &identityScope, &identityKey,
			&identityType, &validFrom, &validTo); err != nil {
			return nil, 0, false, fmt.Errorf("scan registry operation: %w", err)
		}
		if decisionRevision != storedRevision || decisionScope != identityScope || decisionKey != identityKey ||
			decisionType != identityType || validFrom > decisionRevision || validTo != nil && *validTo <= decisionRevision {
			return nil, 0, false, fmt.Errorf("stored registry operation revision is invalid: %w", domain.ErrPersistentIntegrity)
		}
		assignment.Revision = revision
		assignments = append(assignments, assignment)
		payloads = append(payloads, strings.Join([]string{payload, decisionScope, decisionKey, strconv.Itoa(int(decisionType))}, "\x00"))
	}
	if err = rows.Err(); err != nil {
		return nil, 0, false, err
	}
	if len(assignments) == 0 {
		return nil, 0, false, fmt.Errorf("registry operation has no decision rows: %w", domain.ErrPersistentIntegrity)
	}
	if len(assignments) != len(claims) {
		return nil, 0, false, fmt.Errorf("registry operation is partially persisted: %w", domain.ErrPersistentIntegrity)
	}
	for index, claim := range claims {
		expected := strings.Join([]string{claim.PayloadHash, claim.IdentityScope, claim.IdentityKey, strconv.Itoa(int(claim.EntityType))}, "\x00")
		if assignments[index].ProposalKey != claim.ProposalKey || payloads[index] != expected {
			return nil, 0, false, fmt.Errorf("stored registry decision differs from its operation header: %w", domain.ErrPersistentIntegrity)
		}
	}
	return assignments, revision, true, nil
}

func registryClaimsHash(claims []domain.CanonicalIdentityClaim) (string, error) {
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode registry claims: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func allocateCanonicalID(prefix string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("allocate canonical identity: %w", err)
	}
	return prefix + hex.EncodeToString(random), nil
}

func registryDecisionID(corpusID, operationKey string, claim domain.CanonicalIdentityClaim) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{corpusID, operationKey, claim.ProposalKey, claim.PayloadHash}, "\x00")))
	return "resolution:" + hex.EncodeToString(sum[:])
}
