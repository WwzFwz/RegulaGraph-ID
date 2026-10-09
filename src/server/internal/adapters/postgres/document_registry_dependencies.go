// Revalidates a registered complete document's BIND identity dependencies at a
// retained target registry revision. One historical transaction checks the exact
// issuer/regulation/provision natural keys and continuous identity lifetimes; an
// unrelated corpus revision advance does not imply changed document identities.
// This does not refresh alias candidates or semantic decisions, create a reuse
// receipt, or authorize ASSEMBLE publication. SQL batches all IDs and bounds result
// fields/rows. Measure p95, bytes and replan rate under required benchmark targets.
package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (r *Repository) VerifyDocumentRegistryView(ctx context.Context, corpus string, ref *pb.ArtifactRef, raw []byte,
	targetRevision uint64, maximumIdentities int) error {
	if ctx == nil || !storageIDPattern.MatchString(corpus) || targetRevision == 0 || targetRevision > math.MaxInt64 || len(raw) > domain.DefaultWireLimits.MaxBytes {
		return errors.New("bounded registered document and registry target required")
	}
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return err
	}
	if ref.SchemaVersion != 1 || !domain.IsDocumentBatchMediaType(ref.MediaType) || ref.ByteSize == 0 || ref.ByteSize != uint64(len(raw)) || ref.ContentHash.Sha256 != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		return domain.ErrPersistentIntegrity
	}
	registered, err := r.LoadArtifact(ctx, corpus, ref.ArtifactId)
	if err != nil {
		return err
	}
	if !proto.Equal(registered, ref) {
		return domain.ErrPersistentIntegrity
	}
	document := new(pb.DocumentBatch)
	if err = domain.DecodeWire(raw, document, domain.DefaultWireLimits); err != nil {
		return err
	}
	if document.Meta.CorpusId != corpus || document.Context.CorpusId != corpus {
		return domain.ErrPersistentIntegrity
	}
	plan, err := domain.PlanDocumentRegistryDependencies(document, maximumIdentities)
	if err != nil {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = verifyDocumentRegistryDependencies(ctx, tx, corpus, plan, targetRevision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Caller has authenticated bytes and reconstructed a bounded plan. Scheduling
// can reuse this gate inside its own locked transaction instead of trusting a
// successful read that completed before publisher/source state changed.
func verifyDocumentRegistryDependencies(ctx context.Context, query pgx.Tx, corpus string,
	plan domain.DocumentRegistryDependencies, target uint64) error {
	var current, floor int64
	if err := query.QueryRow(ctx, `SELECT registry_revision,registry_history_floor FROM corpus_state WHERE corpus_id=$1`, corpus).Scan(&current, &floor); err != nil {
		return err
	}
	if current <= 0 || floor <= 0 || floor > current {
		return domain.ErrPersistentIntegrity
	}
	if plan.ObservedRevision < uint64(floor) || target < plan.ObservedRevision || target > uint64(current) {
		return fmt.Errorf("document registry revision outside retained history: %w", ErrConflict)
	}
	expected := make(map[string]domain.DocumentRegistryIdentity, len(plan.Identities))
	ids := make([]string, 0, len(plan.Identities))
	for _, identity := range plan.Identities {
		expected[identity.CanonicalID] = identity
		ids = append(ids, identity.CanonicalID)
	}
	rows, err := query.Query(ctx, `SELECT canonical_id,entity_type,
 CASE WHEN octet_length(identity_scope)<=256 THEN identity_scope ELSE '' END,
 CASE WHEN octet_length(identity_key)<=256 THEN identity_key ELSE '' END,
 valid_from_revision,valid_to_revision FROM canonical_identities
 WHERE corpus_id=$1 AND canonical_id=ANY($2::text[]) ORDER BY canonical_id LIMIT $3`, corpus, ids, len(ids)+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var actual domain.DocumentRegistryIdentity
		var from int64
		var to *int64
		if err = rows.Scan(&actual.CanonicalID, &actual.EntityType, &actual.IdentityScope, &actual.IdentityKey, &from, &to); err != nil {
			return err
		}
		want, exists := expected[actual.CanonicalID]
		if !exists || actual != want || from <= 0 || from > int64(plan.ObservedRevision) || to != nil && *to <= int64(target) {
			return fmt.Errorf("document registry identity changed: %w", domain.ErrResolutionReplan)
		}
		delete(expected, actual.CanonicalID)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("document registry identity missing: %w", domain.ErrResolutionReplan)
	}
	return nil
}
