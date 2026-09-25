// Package domain checks the local closure of a complete X01 IndexBatch before
// any backend mutation. It binds every record to the verified source view and
// keeps generation/corpus/counts consistent. The caller still authenticates
// input artifacts, snapshot membership, operation checksum, closure prior
// state, and writer fence. This O(records + closures + version refs) pass must
// be profiled for RSS/p95 against configs/benchmark-targets.yaml.
package domain

import (
	"errors"
	"fmt"
	"math"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ValidateIndexBatchLocalClosure is a preflight gate, not publication approval.
// Rejected work items cannot be silently omitted from a publishable batch.
func ValidateIndexBatchLocalClosure(batch *pb.IndexBatch, source *IndexSourceView, targetSequence uint64, maxOperations int) error {
	if batch == nil || source == nil || targetSequence == 0 || maxOperations <= 0 {
		return errors.New("index batch, verified source view, target sequence and positive operation bound are required")
	}
	if len(batch.Records) > maxOperations || len(batch.Closures) > maxOperations-len(batch.Records) {
		return errors.New("index batch exceeds operation bound")
	}
	if err := ValidateWire(batch, DefaultWireLimits); err != nil {
		return fmt.Errorf("invalid index batch wire: %w", err)
	}
	if err := ValidatePairedIndexGeneration(batch.Generation); err != nil {
		return err
	}
	if batch.Generation.DenseManifest.Task != pb.ModelTask_MODEL_TASK_EMBED {
		return errors.New("index generation requires an embedding model")
	}
	corpus := batch.Meta.CorpusId
	if corpus == "" || corpus != source.corpusID || corpus != batch.Context.CorpusId ||
		corpus != batch.Generation.Meta.CorpusId ||
		batch.Context.SnapshotRef != nil && batch.Context.SnapshotRef.CorpusId != corpus {
		return errors.New("index batch corpus or snapshot context differs from source")
	}
	if batch.Counts.Rejected != 0 || batch.Counts.Expected != uint64(len(batch.Records)) ||
		batch.Counts.Accepted != uint64(len(batch.Records)) {
		return errors.New("index batch accounting is incomplete")
	}
	if len(batch.Records)+len(batch.Closures) == 0 {
		return errors.New("index batch has no operations")
	}
	recordIDs := make(map[string]bool, len(batch.Records))
	chunkIDs := make(map[string]bool, len(batch.Records))
	for _, record := range batch.Records {
		if record == nil || record.Meta == nil {
			return errors.New("nil index record")
		}
		if record.GenerationId != batch.Generation.Meta.RecordId ||
			recordIDs[record.Meta.RecordId] || chunkIDs[record.ChunkId] {
			return errors.New("index record generation or identity collision")
		}
		if record.Meta.Visibility == nil || record.Meta.Visibility.FromSeq != targetSequence ||
			record.Meta.Visibility.ToSeq != nil || record.DenseVector == nil || record.SparseVector == nil ||
			record.DenseVector.ModelId != batch.Generation.DenseManifest.ModelId ||
			record.DenseVector.Dimensions != batch.Generation.DenseManifest.GetDimensions() ||
			len(record.DenseVector.Values) != int(record.DenseVector.Dimensions) ||
			len(record.SparseVector.Indices) == 0 || len(record.SparseVector.Indices) != len(record.SparseVector.Values) {
			return errors.New("index record target or representation differs from generation")
		}
		norm := 0.0
		for _, value := range record.DenseVector.Values {
			norm += float64(value) * float64(value)
		}
		if norm <= 0 || math.IsInf(norm, 0) {
			return errors.New("index record dense vector has invalid norm")
		}
		for i, index := range record.SparseVector.Indices {
			if index == 0 || record.SparseVector.Values[i] <= 0 {
				return errors.New("index record BM25 vector has invalid term or weight")
			}
		}
		if err := source.ValidateRecord(record); err != nil {
			return fmt.Errorf("index record %q: %w", record.Meta.RecordId, err)
		}
		recordIDs[record.Meta.RecordId] = true
		chunkIDs[record.ChunkId] = true
	}
	closures := make(map[string]bool, len(batch.Closures))
	for _, closure := range batch.Closures {
		if closure == nil || closure.ToSeq != targetSequence || closure.ToSeq <= closure.ExpectedFromSeq ||
			closures[closure.RecordId] || recordIDs[closure.RecordId] {
			return errors.New("index closure conflicts with another operation")
		}
		closures[closure.RecordId] = true
	}
	return nil
}
