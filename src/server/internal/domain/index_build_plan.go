// Package domain admits coordinator-planned INDEX output against immutable plan
// and source artifacts. Callers authenticate their bytes and registry/snapshot
// authority first; this gate proves local projection, accounting and the versioned
// vector commitment, not model quality or permission to publish. Work is bounded
// to 128 upserts/128 closures, with one source-view construction. Measure p95/RSS
// and throughput under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"math"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const IndexBuildPlanMediaType = "application/x-protobuf; message=regulagraph.v1.IndexBuildPlan"
const IndexBatchMediaType = "application/x-protobuf; message=regulagraph.v1.IndexBatch"

func ValidateIndexBuildPlan(plan *pb.IndexBuildPlan) error {
	if plan == nil {
		return errors.New("INDEX plan required")
	}
	if err := ValidateWire(plan, DefaultWireLimits); err != nil {
		return err
	}
	corpus := plan.Meta.CorpusId
	if plan.DocumentBatch.ByteSize > uint64(DefaultWireLimits.MaxBytes) {
		return errors.New("INDEX source batch exceeds shared source wire budget")
	}
	if plan.Meta.Visibility != nil || plan.TargetSnapshot.CorpusId != corpus || plan.SourceSnapshot.CorpusId != corpus ||
		plan.SourceSnapshot.Sequence > plan.TargetSnapshot.Sequence ||
		plan.SourceSnapshot.Sequence == plan.TargetSnapshot.Sequence && !proto.Equal(plan.SourceSnapshot, plan.TargetSnapshot) ||
		plan.TargetSnapshot.RepresentationGeneration != plan.Generation.Meta.RecordId || plan.Generation.Meta.CorpusId != corpus ||
		plan.LexicalInputPolicy != "structure-labels-v1" || len(plan.Items) > 128 || len(plan.DictionaryChain) > 64 || len(plan.Closures) > 128 ||
		!proto.Equal(plan.DictionaryChain[len(plan.DictionaryChain)-1], plan.Generation.LexicalDictionary) {
		return errors.New("INDEX plan snapshot, generation, policy or budget mismatch")
	}
	if err := ValidatePairedIndexGeneration(plan.Generation); err != nil {
		return err
	}
	remaining := uint64(64 << 20)
	refs := append([]*pb.ArtifactRef{plan.Generation.LexicalAnalyzer, plan.Generation.LexicalStatistics}, plan.DictionaryChain...)
	for _, ref := range refs {
		if ref.ByteSize == 0 || ref.ByteSize > uint64(DefaultWireLimits.MaxBytes) || ref.ByteSize > remaining {
			return errors.New("INDEX generation exceeds per-artifact or aggregate byte budget")
		}
		remaining -= ref.ByteSize
	}
	if plan.Generation.DenseManifest.Task != pb.ModelTask_MODEL_TASK_EMBED {
		return errors.New("INDEX needs EMBED model")
	}
	chunks, records := map[string]bool{}, map[string]bool{}
	for _, item := range plan.Items {
		if chunks[item.ChunkId] || records[item.RecordId] {
			return errors.New("duplicate INDEX selection")
		}
		chunks[item.ChunkId] = true
		records[item.RecordId] = true
	}
	for _, closure := range plan.Closures {
		if records[closure.RecordId] || closure.ToSeq != plan.TargetSnapshot.Sequence || closure.ExpectedFromSeq >= closure.ToSeq {
			return errors.New("invalid INDEX closure")
		}
		records[closure.RecordId] = true
	}
	return nil
}

// ValidatePlannedIndexBatch is a combined admission gate. The plan/source objects
// must be decoded from hash-verified bytes named by planRef/plan.DocumentBatch.
// A checksum match alone deliberately does not bypass source or plan validation.
func ValidatePlannedIndexBatch(batch *pb.IndexBatch, plan *pb.IndexBuildPlan, planRef *pb.ArtifactRef, source *pb.DocumentBatch) error {
	if err := ValidateIndexBuildPlan(plan); err != nil {
		return err
	}
	if batch == nil || source == nil || planRef == nil {
		return errors.New("INDEX batch, plan reference and source required")
	}
	if err := ValidateWire(planRef, DefaultWireLimits); err != nil {
		return err
	}
	if planRef.ArtifactId != plan.Meta.RecordId || planRef.MediaType != IndexBuildPlanMediaType || !proto.Equal(batch.BuildPlan, planRef) ||
		!proto.Equal(source.Context.GetSnapshotRef(), plan.SourceSnapshot) || source.Meta.GetCorpusId() != plan.Meta.CorpusId ||
		!proto.Equal(batch.Context.GetSnapshotRef(), plan.TargetSnapshot) || !proto.Equal(batch.Generation, plan.Generation) ||
		batch.Meta.GetRecordId() != plan.OutputBatchId || batch.Meta.GetVisibility() != nil ||
		len(batch.Records) != len(plan.Items) || len(batch.Closures) != len(plan.Closures) {
		return errors.New("INDEX output differs from immutable plan/source")
	}
	view, err := NewIndexSourceView(source, 1_000_000)
	if err != nil {
		return err
	}
	if err = ValidateIndexBatchLocalClosure(batch, view, plan.TargetSnapshot.Sequence, 256); err != nil {
		return err
	}
	checkDeps := func(dep *pb.DependencyManifest, id string) bool {
		return dep != nil && dep.ArtifactId == id && proto.Equal(dep.ProducerManifest, plan.Producer) && len(dep.LookupScopeRevisions) == 0 &&
			len(dep.Dependencies) == 1 && dep.Dependencies[0].DependencyId == planRef.ArtifactId && proto.Equal(dep.Dependencies[0].Fingerprint, planRef.ContentHash)
	}
	if !checkDeps(batch.Dependencies, plan.OutputBatchId) {
		return errors.New("INDEX batch dependencies differ from plan")
	}
	for i, record := range batch.Records {
		if record.Meta.RecordId != plan.Items[i].RecordId || record.ChunkId != plan.Items[i].ChunkId || !checkDeps(record.Dependencies, record.Meta.RecordId) {
			return errors.New("INDEX record selection/dependencies differ from plan")
		}
		norm := 0.0
		for _, v := range record.DenseVector.Values {
			norm += float64(v) * float64(v)
		}
		if math.Abs(norm-1) > 0.01 || math.IsNaN(norm) {
			return errors.New("INDEX dense output is not unit normalized")
		}
	}
	for i, closure := range batch.Closures {
		if !proto.Equal(closure, plan.Closures[i]) {
			return errors.New("INDEX closure differs from plan")
		}
	}
	checksum, err := IndexPlanOperationsChecksum(batch)
	if err != nil {
		return err
	}
	if checksum != batch.OperationsChecksum.Sha256 {
		return errors.New("INDEX operations checksum mismatch")
	}
	return nil
}

// IndexPlanOperationsChecksum implements doc/index-build.md's canonical v1
// commitment, shared with Rust. Source-derived facts MUST be checked separately
// against the plan/source; no arbitrary protobuf serialization is hashed here.
func IndexPlanOperationsChecksum(batch *pb.IndexBatch) (string, error) {
	if batch == nil || batch.BuildPlan.GetContentHash().GetSha256() == "" {
		return "", errors.New("INDEX checksum requires plan")
	}
	h := sha256.New()
	h.Write([]byte("regulagraph-index-plan-v1\x00"))
	indexHashString(h, batch.BuildPlan.ContentHash.Sha256)
	indexHashU64(h, uint64(len(batch.Records)))
	for _, record := range batch.Records {
		if record == nil || record.Meta == nil || record.DenseVector == nil || record.SparseVector == nil || len(record.SparseVector.Indices) != len(record.SparseVector.Values) {
			return "", errors.New("incomplete INDEX checksum input")
		}
		indexHashString(h, record.Meta.RecordId)
		indexHashString(h, record.ChunkId)
		indexHashU64(h, uint64(len(record.DenseVector.Values)))
		for _, v := range record.DenseVector.Values {
			indexHashU32(h, math.Float32bits(v))
		}
		indexHashU64(h, uint64(len(record.SparseVector.Indices)))
		for i, id := range record.SparseVector.Indices {
			indexHashU32(h, id)
			indexHashU32(h, math.Float32bits(record.SparseVector.Values[i]))
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func indexHashString(h hash.Hash, s string) { indexHashU64(h, uint64(len(s))); h.Write([]byte(s)) }
func indexHashU64(h hash.Hash, n uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	h.Write(b[:])
}
func indexHashU32(h hash.Hash, n uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], n)
	h.Write(b[:])
}
