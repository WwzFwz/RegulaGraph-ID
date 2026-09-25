// Point projection sends validated paired legal facts and named vectors to a
// verified physical collection. The caller must prove source artifact hash,
// snapshot membership, operation fence and point-ID allocation before calling
// Upsert; an acknowledged write is not a publication receipt. Bound batch size
// and bytes to profile throughput, p95/p99 and memory without unbounded JSON.
package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

var pointUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Point struct {
	ID     string // UUID allocated and collision-checked by the trusted catalog.
	Record *pb.IndexRecord
}

type pointPayload struct {
	CorpusID         string          `json:"corpus_id"`
	GenerationID     string          `json:"generation_id"`
	RecordID         string          `json:"record_id"`
	ChunkID          string          `json:"chunk_id"`
	FromSeq          uint64          `json:"from_seq"`
	ToSeq            *uint64         `json:"to_seq,omitempty"`
	ProvisionFilters []pairedPayload `json:"provision_filters"`
}

type pairedPayload struct {
	VersionID     string          `json:"provision_version_id"`
	RegulationID  string          `json:"regulation_id"`
	SourceBlobID  string          `json:"source_blob_id"`
	Jurisdiction  string          `json:"jurisdiction"`
	LegalStatus   string          `json:"legal_status"`
	LegalInterval json.RawMessage `json:"legal_interval"`
}

type qdrantPoint struct {
	ID      string         `json:"id"`
	Vector  map[string]any `json:"vector"`
	Payload pointPayload   `json:"payload"`
}

// Upsert writes at most 256 complete records per call. Qdrant's wait+strong
// acknowledgment is checked, but readback/replica routing still require a
// separate readiness proof before the generation may become query-visible.
func (store *Store) Upsert(ctx context.Context, points []Point) error {
	if err := store.requireReady(); err != nil {
		return err
	}
	if len(points) == 0 || len(points) > maxPointsPerWrite {
		return errors.New("qdrant upsert point count is outside budget")
	}
	projected := make([]qdrantPoint, 0, len(points))
	ids := make(map[string]bool, len(points))
	encodedBudget := 0
	for _, point := range points {
		if point.Record == nil {
			return errors.New("qdrant point record is required")
		}
		encodedBudget += proto.Size(point.Record)
		if encodedBudget > 1<<20 {
			return errors.New("qdrant upsert aggregate record budget exceeded")
		}
		canonicalID := strings.ToLower(point.ID)
		if !pointUUID.MatchString(point.ID) || ids[canonicalID] {
			return errors.New("qdrant point ID is invalid or duplicated")
		}
		ids[canonicalID] = true
		item, err := store.projectPoint(point)
		if err != nil {
			return err
		}
		projected = append(projected, item)
	}
	var result struct {
		Status      string  `json:"status"`
		OperationID *uint64 `json:"operation_id"`
	}
	status, err := store.call(ctx, http.MethodPut,
		"/collections/"+store.collection+"/points?wait=true&ordering=strong",
		map[string]any{"points": projected}, &result)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || result.Status != "completed" || result.OperationID == nil {
		return errors.New("qdrant upsert did not complete")
	}
	return nil
}

func (store *Store) projectPoint(point Point) (qdrantPoint, error) {
	record := point.Record
	if err := domain.ValidatePairedIndexFilters(record); err != nil {
		return qdrantPoint{}, fmt.Errorf("qdrant index record: %w", err)
	}
	if record.Meta.CorpusId != store.binding.CorpusID ||
		record.GenerationId != store.binding.Generation.Meta.RecordId ||
		record.DenseVector == nil || record.SparseVector == nil ||
		record.DenseVector.ModelId != store.binding.Generation.DenseManifest.ModelId ||
		record.DenseVector.Dimensions != store.binding.Generation.DenseManifest.GetDimensions() ||
		len(record.DenseVector.Values) != int(record.DenseVector.Dimensions) ||
		len(record.SparseVector.Indices) == 0 ||
		len(record.SparseVector.Indices) != len(record.SparseVector.Values) {
		return qdrantPoint{}, errors.New("qdrant record generation, model, dimension or sparse shape mismatch")
	}
	if record.Meta.Visibility.FromSeq == 0 || record.Meta.Visibility.FromSeq > maxExactFilterSequence ||
		record.Meta.Visibility.ToSeq != nil && *record.Meta.Visibility.ToSeq > maxExactFilterSequence {
		return qdrantPoint{}, errors.New("qdrant visibility exceeds exact numeric filter range")
	}
	norm := 0.0
	for _, value := range record.DenseVector.Values {
		norm += float64(value) * float64(value)
	}
	if norm <= 0 || math.IsInf(norm, 0) {
		return qdrantPoint{}, errors.New("qdrant cosine vector has zero or invalid norm")
	}
	for i, value := range record.SparseVector.Values {
		if record.SparseVector.Indices[i] == 0 || i > 0 && record.SparseVector.Indices[i] <= record.SparseVector.Indices[i-1] || value <= 0 {
			return qdrantPoint{}, errors.New("qdrant BM25 sparse vector requires sorted unique positive term IDs and weights")
		}
	}
	filters := make([]pairedPayload, 0, len(record.FilterMetadata.ProvisionFilters))
	for _, filter := range record.FilterMetadata.ProvisionFilters {
		interval, err := protojson.Marshal(filter.LegalInterval)
		if err != nil {
			return qdrantPoint{}, err
		}
		filters = append(filters, pairedPayload{
			VersionID: filter.ProvisionVersionId, RegulationID: filter.RegulationId,
			SourceBlobID: filter.SourceBlobId, Jurisdiction: filter.Jurisdiction,
			LegalStatus: filter.LegalStatus.String(), LegalInterval: json.RawMessage(interval),
		})
	}
	return qdrantPoint{
		ID: point.ID,
		Vector: map[string]any{
			"dense": record.DenseVector.Values,
			"bm25":  map[string]any{"indices": record.SparseVector.Indices, "values": record.SparseVector.Values},
		},
		Payload: pointPayload{CorpusID: record.Meta.CorpusId,
			GenerationID: record.GenerationId, RecordID: record.Meta.RecordId,
			ChunkID: record.ChunkId, FromSeq: record.Meta.Visibility.FromSeq,
			ToSeq: record.Meta.Visibility.ToSeq, ProvisionFilters: filters},
	}, nil
}
