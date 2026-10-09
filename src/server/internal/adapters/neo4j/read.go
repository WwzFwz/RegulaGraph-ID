// Reads exact typed records from a sealed, PostgreSQL-admitted graph generation.
// Every batch repeats the seal/binding check and honors the reader lease deadline.
// Two-phase type/size/hash admission bounds Bolt payload transfer even if record
// properties change between statements; final comparison rejects projection drift.
// Cypher types byte arrays as integer lists: a corrupt integer list can transfer
// at most nine wire bytes per admitted element (plus framing), then is rejected by
// the Go []byte check. maximumBytes bounds encoded protobuf bytes, not total RSS.
// This adapter does not authorize callers, select legal versions or interpret paths.
// Batch at most 256 IDs/16MiB; measure round trips, queue and p95/RSS under required
// GRAPH/RETRIEVAL benchmarks. No schema creation or write occurs in this path.
package neo4j

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type Reader struct {
	store          *Store
	operationsHash string
	operations     uint64
	expires        time.Time
	snapshot       *pb.SnapshotRef
}

// Keep both stages guarded: a sealed generation may still be corrupted outside
// this adapter. size() alone counts list elements and cannot bound string lists.
const graphReadMetadata = `UNWIND $ids AS id OPTIONAL MATCH (n:RGRecord {corpus:$corpus,generation:$generation,id:id})
 RETURN id,n.kind=$kind AND n.from_seq=$sequence AS valid,
 CASE WHEN valueType(n.hash)='STRING NOT NULL' THEN
   CASE WHEN size(n.hash)=64 AND n.hash =~ '^[0-9a-f]{64}$' THEN n.hash END END AS hash,
 CASE WHEN valueType(n.payload)='LIST<INTEGER NOT NULL> NOT NULL' THEN size(n.payload) END AS bytes`

const graphReadPayload = `UNWIND $selection AS item OPTIONAL MATCH (n:RGRecord {corpus:$corpus,generation:$generation,id:item.id})
 RETURN item.id,CASE WHEN valueType(n.payload)='LIST<INTEGER NOT NULL> NOT NULL' THEN
   CASE WHEN n.kind=$kind AND n.from_seq=$sequence AND n.hash=item.hash AND size(n.payload)=item.bytes THEN n.payload END END`

func (s *Store) OpenReader(ctx context.Context, admitted *domain.PinnedGraph) (*Reader, error) {
	if ctx == nil || admitted == nil || admitted.Snapshot == nil || admitted.AuthScope == "" {
		return nil, errors.New("published graph admission required")
	}
	c := admitted.Catalog
	if err := domain.ValidateGraphCatalogBinding(c); err != nil {
		return nil, err
	}
	if err := domain.ValidateWire(admitted.Snapshot, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if c.Endpoint != s.endpoint || c.Database != s.database || c.BindingHash != s.bindingHash || admitted.Pin.CorpusID != s.binding.CorpusID || admitted.Pin.SnapshotID != admitted.Snapshot.SnapshotId || admitted.Pin.Sequence != s.binding.Sequence || admitted.Snapshot.CorpusId != s.binding.CorpusID || admitted.Snapshot.Sequence != s.binding.Sequence || admitted.Pin.LeaseID == "" || admitted.Pin.OwnerID == "" || admitted.Pin.ExpiresAt.IsZero() {
		return nil, ErrGraphConflict
	}
	r := &Reader{store: s, operationsHash: c.OperationsHash, operations: c.Operations, expires: admitted.Pin.ExpiresAt, snapshot: proto.Clone(admitted.Snapshot).(*pb.SnapshotRef)}
	ctx, cancel := context.WithDeadline(ctx, r.expires)
	defer cancel()
	if err := s.transaction(ctx, false, r.verifySeal); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Reader) verifySeal(ctx context.Context, tx bolt.ExplicitTransaction) error {
	params := r.store.params()
	params["operations_hash"], params["operations"] = r.operationsHash, int64(r.operations)
	row, err := one(ctx, tx, `MATCH (g:RGGeneration {corpus:$corpus,generation:$generation})
 RETURN g.binding=$binding AND g.state='SEALED' AND g.sequence=$sequence AND g.operations_hash=$operations_hash AND g.operations=$operations`, params)
	if err != nil {
		return err
	}
	if row.Values[0] != true {
		return ErrGraphConflict
	}
	return nil
}

// kind determines the concrete protobuf type: entity, mention, assertion,
// support, or decision. Output order equals requested ID order; missing/corrupt
// records fail the entire selection. Callers must recheck their PostgreSQL pin
// before exposing evidence because an in-flight lease may be revoked explicitly.
func (r *Reader) ReadRecords(ctx context.Context, kind string, ids []string, maximumBytes uint64) ([]proto.Message, error) {
	return r.readRecords(ctx, kind, ids, maximumBytes, nil)
}

// actualBytes is written only on success and counts the wire payload rather than
// reserialized protobuf size (noncanonical protobuf can decode to fewer bytes).
func (r *Reader) readRecords(ctx context.Context, kind string, ids []string, maximumBytes uint64, actualBytes *uint64) ([]proto.Message, error) {
	if r == nil || r.store == nil || ctx == nil || len(ids) == 0 || len(ids) > 256 || maximumBytes == 0 || maximumBytes > 16<<20 {
		return nil, errors.New("bounded graph read selection required")
	}
	if newGraphRecord(kind) == nil {
		return nil, errors.New("unknown graph record kind")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: r.store.binding.CorpusID, RecordId: id}, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, errors.New("duplicate graph record selection")
		}
		seen[id] = true
	}
	ctx, cancel := context.WithDeadline(ctx, r.expires)
	defer cancel()
	var out []proto.Message
	var transferred uint64
	err := r.store.transaction(ctx, false, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
		if err := r.verifySeal(ctx, tx); err != nil {
			return err
		}
		p := r.store.params()
		p["ids"], p["kind"] = ids, kind
		rows, err := tx.Run(ctx, graphReadMetadata, p)
		if err != nil {
			return err
		}
		type recordSize struct {
			hash string
			size int64
		}
		metadata := map[string]recordSize{}
		remaining := maximumBytes
		for rows.Next(ctx) {
			row := rows.Record()
			id, idOK := row.Values[0].(string)
			hash, hashOK := row.Values[2].(string)
			size, sizeOK := row.Values[3].(int64)
			if !idOK || !seen[id] || row.Values[1] != true || !hashOK || !sizeOK || size <= 0 || metadata[id].size != 0 {
				return ErrGraphConflict
			}
			if uint64(size) > remaining {
				return domain.ErrGraphReadBudget
			}
			metadata[id] = recordSize{hash, size}
			remaining -= uint64(size)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(metadata) != len(ids) {
			return ErrGraphConflict
		}
		transferred = maximumBytes - remaining
		selection := make([]any, 0, len(ids))
		for _, id := range ids {
			m := metadata[id]
			selection = append(selection, map[string]any{"id": id, "hash": m.hash, "bytes": m.size})
		}
		p["selection"] = selection
		rows, err = tx.Run(ctx, graphReadPayload, p)
		if err != nil {
			return err
		}
		values := map[string]proto.Message{}
		projected := make([]any, 0, len(ids))
		for rows.Next(ctx) {
			row := rows.Record()
			id, idOK := row.Values[0].(string)
			raw, rawOK := row.Values[1].([]byte)
			if !idOK || !seen[id] || !rawOK || int64(len(raw)) != metadata[id].size || fmt.Sprintf("%x", sha256.Sum256(raw)) != metadata[id].hash || values[id] != nil {
				return ErrGraphConflict
			}
			value := newGraphRecord(kind)
			if domain.DecodeWire(raw, value, domain.DefaultWireLimits) != nil {
				return ErrGraphConflict
			}
			meta := value.(graphRecord).GetMeta()
			if meta.RecordId != id || meta.CorpusId != r.store.binding.CorpusID || meta.SchemaVersion != 1 || meta.Visibility == nil || meta.Visibility.FromSeq != r.store.binding.Sequence || meta.Visibility.ToSeq != nil {
				return ErrGraphConflict
			}
			props := map[string]any{"id": id, "corpus": meta.CorpusId, "generation": r.store.binding.Generation, "kind": kind, "hash": metadata[id].hash, "payload": raw, "from_seq": int64(meta.Visibility.FromSeq)}
			switch v := value.(type) {
			case *pb.CanonicalEntity:
				if v.RegistryRevision > r.store.binding.RegistryRevision {
					return ErrGraphConflict
				}
				props["entity_type"], props["label"], props["scope"] = v.EntityType, v.PreferredLabel, v.Scope
			case *pb.RelationAssertion:
				props["predicate"], props["subject"], props["object"] = v.PredicateId, v.SubjectId, v.ObjectId
			case *pb.SupportRecord:
				props["assertion"], props["source_group"] = v.AssertionId, v.IndependentSourceGroup
			case *pb.ResolutionDecision:
				if v.RegistryRevision > r.store.binding.RegistryRevision {
					return ErrGraphConflict
				}
			}
			values[id] = value
			projected = append(projected, props)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(values) != len(ids) {
			return ErrGraphConflict
		}
		p["records"] = projected
		checked, err := one(ctx, tx, `UNWIND $records AS expected OPTIONAL MATCH (n:RGRecord {corpus:$corpus,generation:$generation,id:expected.id})
 RETURN count(*),sum(CASE WHEN properties(n)=expected THEN 1 ELSE 0 END)`, p)
		if err != nil {
			return err
		}
		if checked.Values[0] != int64(len(ids)) || checked.Values[1] != int64(len(ids)) {
			return ErrGraphConflict
		}
		if err = r.verifySeal(ctx, tx); err != nil {
			return err
		}
		for _, id := range ids {
			out = append(out, values[id])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if actualBytes != nil {
		*actualBytes = transferred
	}
	return out, nil
}

func newGraphRecord(kind string) proto.Message {
	switch kind {
	case "entity":
		return new(pb.CanonicalEntity)
	case "mention":
		return new(pb.Mention)
	case "assertion":
		return new(pb.RelationAssertion)
	case "support":
		return new(pb.SupportRecord)
	case "decision":
		return new(pb.ResolutionDecision)
	default:
		return nil
	}
}
