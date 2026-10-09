// Reserves immutable Neo4j routing and the exact output write set together with
// its durable planned operation. The source-admitted graph authority holds all
// publication/registry/source/child/pin locks through commit; stale prepared values
// cannot reserve a generation. Route or bytes cannot change on retry. Catalog
// presence is not readiness/serving permission. Measure admission/lock/intent p95
// and RSS under configs/benchmark-targets.yaml; acceptance remains unmeasured.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

// GraphWriteOperation identifies one whole-generation command, not each delta.
func GraphWriteOperation(publication string) string {
	h := sha256.Sum256([]byte("regulagraph-graph-write-v1\x00" + publication))
	return fmt.Sprintf("graph-write:%x", h)
}

func (admission *GraphJobAdmission) ReserveGraphGeneration(ctx context.Context, pin domain.SnapshotPin, expected domain.CompletedGraphInventory, binding domain.GraphCatalogBinding) error {
	if err := domain.ValidateCompletedGraphInventory(expected); err != nil {
		return err
	}
	if err := domain.ValidateGraphCatalogBinding(binding); err != nil {
		return err
	}
	if admission == nil || admission.repository == nil || binding.InventoryHash != admission.digest {
		return ErrConflict
	}
	first := expected.Inventory.Assignments[0].Plan
	b := binding.Binding
	if b.PublicationID != first.PublicationId || b.CorpusID != first.Meta.CorpusId || b.Fence != first.PublicationFence || b.Sequence != first.TargetSequence || b.RegistryRevision != first.RegistryRevision || !proto.Equal(b.BaseSnapshot, first.Context.SnapshotRef) || len(binding.Outputs) != len(expected.Outputs) {
		return ErrConflict
	}
	for i, ref := range binding.Outputs {
		if !proto.Equal(ref, expected.Outputs[i]) {
			return ErrConflict
		}
	}
	prepared, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	if len(raw) > domain.DefaultWireLimits.MaxBytes {
		return ErrResultLimit
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	_, err = admission.withCompletedGraph(ctx, pin, func(ctx context.Context, tx pgx.Tx, current domain.CompletedGraphInventory) error {
		actual, e := json.Marshal(current)
		if e != nil {
			return e
		}
		if !bytes.Equal(actual, prepared) {
			return ErrConflict
		}
		var manifestRaw []byte
		if e = tx.QueryRow(ctx, `SELECT manifest_payload FROM snapshots WHERE publication_id=$1`, b.PublicationID).Scan(&manifestRaw); e != nil {
			return e
		}
		manifest, e := unmarshalManifest(manifestRaw)
		if e != nil {
			return e
		}
		if len(manifest.Closures) != 0 || !proto.Equal(manifest.ParentRef, b.BaseSnapshot) {
			return ErrConflict
		}
		var generation, checksum string
		var count int64
		e = tx.QueryRow(ctx, `SELECT generation,operations_checksum,expected_count FROM publication_backends WHERE publication_id=$1 AND backend=$2`, b.PublicationID, int16(pb.BackendKind_BACKEND_KIND_NEO4J)).Scan(&generation, &checksum, &count)
		if e != nil {
			return e
		}
		if generation != b.Generation || checksum != binding.OperationsHash || uint64(count) != binding.Records+binding.Edges {
			return ErrConflict
		}
		_, e = tx.Exec(ctx, `INSERT INTO graph_generations(publication_id,corpus_id,generation_id,fence,endpoint,database_name,payload,payload_hash)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, b.PublicationID, b.CorpusID, b.Generation, int64(b.Fence), binding.Endpoint, binding.Database, raw, digest)
		if e != nil {
			return e
		}
		stored, e := loadGraphCatalog(ctx, tx, b.CorpusID, b.PublicationID)
		if e != nil {
			return e
		}
		storedRaw, e := json.Marshal(stored)
		if e != nil {
			return e
		}
		if !bytes.Equal(raw, storedRaw) {
			return ErrConflict
		}
		operation := GraphWriteOperation(b.PublicationID)
		_, e = tx.Exec(ctx, `INSERT INTO publication_operations(operation_key,publication_id,backend,payload_hash,fence,status)
 VALUES($1,$2,$3,$4,$5,'planned') ON CONFLICT(operation_key) DO NOTHING`, operation, b.PublicationID, int16(pb.BackendKind_BACKEND_KIND_NEO4J), binding.OperationsHash, int64(b.Fence))
		if e != nil {
			return e
		}
		var pub, hash, status string
		var fence int64
		var backend int16
		e = tx.QueryRow(ctx, `SELECT publication_id,backend,payload_hash,fence,status FROM publication_operations WHERE operation_key=$1`, operation).Scan(&pub, &backend, &hash, &fence, &status)
		if e != nil {
			return e
		}
		if pub != b.PublicationID || backend != int16(pb.BackendKind_BACKEND_KIND_NEO4J) || hash != binding.OperationsHash || uint64(fence) != b.Fence || status != "planned" && status != "applied" {
			return ErrConflict
		}
		return nil
	})
	return err
}

// LoadGraphGeneration is a historical catalog read, never a serving admission.
func (r *Repository) LoadGraphGeneration(ctx context.Context, corpus, publication string) (domain.GraphCatalogBinding, error) {
	if !storageIDPattern.MatchString(corpus) || !storageIDPattern.MatchString(publication) {
		return domain.GraphCatalogBinding{}, errors.New("graph catalog identity required")
	}
	return loadGraphCatalog(ctx, r.pool, corpus, publication)
}

func loadGraphCatalog(ctx context.Context, q indexQuerier, corpus, publication string) (domain.GraphCatalogBinding, error) {
	var out domain.GraphCatalogBinding
	var raw []byte
	var digest, generation, endpoint, database string
	var fence int64
	err := q.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload)<=16777216 THEN payload ELSE NULL END,payload_hash,generation_id,fence,endpoint,database_name FROM graph_generations WHERE corpus_id=$1 AND publication_id=$2`, corpus, publication).Scan(&raw, &digest, &generation, &fence, &endpoint, &database)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if len(raw) == 0 || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
		return out, domain.ErrPersistentIntegrity
	}
	var result domain.GraphCatalogBinding
	if json.Unmarshal(raw, &result) != nil || domain.ValidateGraphCatalogBinding(result) != nil {
		return out, domain.ErrPersistentIntegrity
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(canonical, raw) || result.Binding.CorpusID != corpus || result.Binding.PublicationID != publication || result.Binding.Generation != generation || result.Binding.Fence != uint64(fence) || result.Endpoint != endpoint || result.Database != database {
		return out, domain.ErrPersistentIntegrity
	}
	return result, nil
}
