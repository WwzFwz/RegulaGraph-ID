// Exports a bounded canonical selection at the immutable registry revision bound
// to a publication. One read transaction verifies fence/corpus/history and hashes
// every stored profile against its indexed identity. Missing rows fail the whole
// selection; no label/type is guessed from a canonical ID. Caller authenticates
// corpus access and persists returned C01 bytes through the artifact store. This
// read neither approves resolution decisions nor publishes a graph. Profile SQL
// caps total payload bytes before transfer; measure p95/p99, pool wait and RSS
// against configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// RegistryEntityExport holds internal control-plane arguments, not another wire schema.
type RegistryEntityExport struct {
	ViewID, CorpusID, PublicationID string
	Fence, Revision                 uint64
	EntityIDs                       []string
	Producer                        *pb.ProducerManifest
	MaximumEntities, MaximumBytes   int
}

func (r *Repository) ExportRegistryEntityView(ctx context.Context, request RegistryEntityExport) (*pb.RegistryEntityView, error) {
	if !storageIDPattern.MatchString(request.ViewID) || !storageIDPattern.MatchString(request.CorpusID) || !storageIDPattern.MatchString(request.PublicationID) ||
		request.Fence == 0 || request.Fence > math.MaxInt64 || request.Revision == 0 || request.Revision > math.MaxInt64 ||
		request.MaximumEntities <= 0 || request.MaximumEntities > domain.DefaultWireLimits.MaxItems || len(request.EntityIDs) > request.MaximumEntities ||
		request.MaximumBytes <= 0 || request.MaximumBytes > domain.DefaultWireLimits.MaxBytes || request.Producer == nil || request.Producer.SchemaVersion != 1 {
		return nil, errors.New("bounded registry export and publication binding required")
	}
	if err := domain.ValidateWire(request.Producer, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	ids := slices.Clone(request.EntityIDs)
	slices.Sort(ids)
	selected := make(map[string]bool, len(ids))
	for i, id := range ids {
		if !storageIDPattern.MatchString(id) || id == request.ViewID || i > 0 && ids[i-1] == id {
			return nil, errors.New("invalid or duplicate registry selection ID")
		}
		selected[id] = false
	}
	view := &pb.RegistryEntityView{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: request.CorpusID, RecordId: request.ViewID},
		PublicationId: request.PublicationID, PublicationFence: request.Fence, RegistryRevision: request.Revision,
		RequestedIds: ids, ProducerManifest: proto.Clone(request.Producer).(*pb.ProducerManifest)}
	remaining := request.MaximumBytes - proto.Size(view)
	if remaining < 0 {
		return nil, ErrResultLimit
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var corpus string
	var fence, revision, floor, current, snapshotFence, publisherFence int64
	var state int16
	err = tx.QueryRow(ctx, `SELECT b.corpus_id,b.fence,b.registry_revision,c.registry_history_floor,c.registry_revision,s.fence,s.state,c.publisher_fence
 FROM snapshot_registry_bindings b JOIN corpus_state c ON c.corpus_id=b.corpus_id
 JOIN snapshots s ON s.publication_id=b.publication_id AND s.corpus_id=b.corpus_id WHERE b.publication_id=$1`, request.PublicationID).
		Scan(&corpus, &fence, &revision, &floor, &current, &snapshotFence, &state, &publisherFence)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if corpus != request.CorpusID || uint64(fence) != request.Fence || snapshotFence != fence || uint64(revision) != request.Revision || revision < floor || revision > current ||
		state != int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED) && publisherFence != fence ||
		(state != int16(pb.SnapshotState_SNAPSHOT_STATE_STAGING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_VALIDATING) && state != int16(pb.SnapshotState_SNAPSHOT_STATE_PUBLISHED)) {
		return nil, fmt.Errorf("registry export binding is unavailable: %w", ErrConflict)
	}
	rows, err := tx.Query(ctx, `WITH selected AS (
 SELECT p.canonical_id,p.entity_type,p.canonical_scope,p.preferred_label,p.review_state,p.from_revision,
 p.profile_payload,p.record_hash,i.entity_type AS identity_type,i.identity_scope,i.identity_key,i.valid_from_revision,i.valid_to_revision,
 SUM(octet_length(p.profile_payload)+16) OVER () AS total_bytes
 FROM registry_entity_profiles p JOIN canonical_identities i ON i.corpus_id=p.corpus_id AND i.canonical_id=p.canonical_id
 WHERE p.corpus_id=$1 AND p.canonical_id=ANY($2::text[]) AND p.from_revision<=$3 AND (p.to_revision IS NULL OR p.to_revision>$3)
 ) SELECT canonical_id,entity_type,canonical_scope,preferred_label,review_state,from_revision,
 CASE WHEN total_bytes<=$4 THEN profile_payload ELSE NULL END,record_hash,identity_type,identity_scope,identity_key,valid_from_revision,valid_to_revision,total_bytes
 FROM selected ORDER BY canonical_id COLLATE "C" LIMIT $5`, request.CorpusID, ids, int64(request.Revision), remaining, len(ids)+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, entityType, scope, label, digest string
		var review int16
		var from, total int64
		var raw []byte
		var identity aliasIdentity
		if err = rows.Scan(&id, &entityType, &scope, &label, &review, &from, &raw, &digest, &identity.entityType, &identity.identityScope, &identity.identityKey, &identity.fromRevision, &identity.toRevision, &total); err != nil {
			return nil, err
		}
		if total > int64(remaining) {
			return nil, ErrResultLimit
		}
		seen, requested := selected[id]
		if !requested || seen || from <= 0 || from > revision || identity.fromRevision <= 0 || identity.fromRevision > from || identity.toRevision != nil && *identity.toRevision <= revision || aliasEntityType(entityType) != identity.entityType {
			return nil, fmt.Errorf("canonical view identity is absent or invalid: %w", domain.ErrPersistentIntegrity)
		}
		selected[id] = true
		entity := new(pb.CanonicalEntity)
		if err = decodeRegistryPayload(raw, digest, entity); err != nil {
			return nil, err
		}
		if entity.GetMeta().GetRecordId() != id || entity.GetMeta().GetCorpusId() != corpus || entity.EntityType != entityType || entity.Scope != scope || entity.PreferredLabel != label || int16(entity.ReviewState) != review || !profileContainsExactIdentity(entity, identity) {
			return nil, fmt.Errorf("canonical view profile differs from registry columns: %w", domain.ErrPersistentIntegrity)
		}
		entity.RegistryRevision = uint64(from)
		view.Entities = append(view.Entities, entity)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(view.Entities) != len(ids) {
		return nil, fmt.Errorf("canonical selection is incomplete: %w", ErrNotFound)
	}
	if err = domain.ValidateRegistryEntityView(view, request.MaximumEntities, request.MaximumBytes); err != nil {
		return nil, fmt.Errorf("registry entity view rejected: %w", domain.ErrPersistentIntegrity)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return view, nil
}
