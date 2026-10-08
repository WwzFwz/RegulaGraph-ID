// Plans bounded INDEX batches from an explicitly selected, authenticated CHUNK
// inventory. All chunks are covered exactly once; source checkpoints, snapshot,
// lexical population and dictionary authority are checked before any model call.
// IDs and ordering are deterministic within this Go producer version. Returned
// plans are copies; the private inventory binds later output admission. This is
// coordinator preparation, not a durable scheduler, corpus discovery or graph
// readiness proof. Persist plans before dispatch and retain the selected inventory
// across retries. Measure preparation RSS/p95 and batch counts against
// configs/benchmark-targets.yaml; quality/performance remain REQUIRED_UNMEASURED.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type InitialIndexSource struct {
	SourceJobID   string
	DocumentBatch *pb.ArtifactRef
}

type InitialIndexPlanConfig struct {
	Binding         domain.IndexCatalogBinding
	Snapshot        *pb.SnapshotRef
	Producer        *pb.ProducerManifest
	DictionaryChain []*pb.ArtifactRef
	AuthScope       string
	ChunksPerBatch  int
}

type PlannedInitialBatch struct {
	SourceJobID string
	Plan        *pb.IndexBuildPlan
	Reference   *pb.ArtifactRef
}

// InitialIndexPlans can only be constructed through verified source admission.
// It owns its config and plans independently of both input and returned messages.
type InitialIndexPlans struct {
	binding   domain.IndexCatalogBinding
	authScope string
	plans     []PlannedInitialBatch
}

func (p *InitialIndexPlans) Batches() []PlannedInitialBatch {
	if p == nil {
		return nil
	}
	out := make([]PlannedInitialBatch, len(p.plans))
	for i, batch := range p.plans {
		out[i] = PlannedInitialBatch{batch.SourceJobID, proto.Clone(batch.Plan).(*pb.IndexBuildPlan), proto.Clone(batch.Reference).(*pb.ArtifactRef)}
	}
	return out
}

// PlanInitialIndex requires a trusted operator-selected inventory. It never
// infers that arbitrary submitted jobs represent every document in the corpus.
// Limits match the initial writer (256 outputs/64 MiB source+lexical bytes).
func PlanInitialIndex(ctx context.Context, authority IndexAuthority, reader IndexArtifactReader, cfg InitialIndexPlanConfig, inputs []InitialIndexSource) (*InitialIndexPlans, error) {
	if ctx == nil || authority == nil || reader == nil || len(inputs) == 0 || len(inputs) > 256 || cfg.AuthScope == "" || cfg.ChunksPerBatch < 1 || cfg.ChunksPerBatch > 128 {
		return nil, errors.New("bounded initial index sources, scope and batch size required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidateIndexCatalogBinding(cfg.Binding); err != nil {
		return nil, err
	}
	for _, m := range []proto.Message{cfg.Snapshot, cfg.Producer} {
		if err := domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	if cfg.Snapshot.CorpusId != cfg.Binding.Generation.Meta.CorpusId || cfg.Snapshot.RepresentationGeneration != cfg.Binding.Generation.Meta.RecordId || cfg.Snapshot.Sequence > 1<<53-1 || len(cfg.DictionaryChain) == 0 || len(cfg.DictionaryChain) > 64 {
		return nil, errors.New("initial index snapshot/generation/dictionary mismatch")
	}
	cfg.Binding.Generation = proto.Clone(cfg.Binding.Generation).(*pb.IndexGeneration)
	cfg.Snapshot = proto.Clone(cfg.Snapshot).(*pb.SnapshotRef)
	cfg.Producer = proto.Clone(cfg.Producer).(*pb.ProducerManifest)
	chain := make([]*pb.ArtifactRef, len(cfg.DictionaryChain))
	for i, ref := range cfg.DictionaryChain {
		if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		chain[i] = proto.Clone(ref).(*pb.ArtifactRef)
	}
	if !proto.Equal(chain[len(chain)-1], cfg.Binding.Generation.LexicalDictionary) {
		return nil, errors.New("dictionary chain does not end at index generation")
	}
	ordered := append([]InitialIndexSource(nil), inputs...)
	for i, input := range ordered {
		if err := domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.Snapshot.CorpusId, RecordId: input.SourceJobID}, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if err := domain.ValidateWire(input.DocumentBatch, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		ordered[i].DocumentBatch = proto.Clone(input.DocumentBatch).(*pb.ArtifactRef)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].DocumentBatch.ArtifactId < ordered[j].DocumentBatch.ArtifactId })
	loader := &initialArtifactLoader{authority: authority, reader: reader, corpus: cfg.Snapshot.CorpusId, remaining: 64 << 20, cache: map[string]initialArtifact{}}
	result := &InitialIndexPlans{binding: cfg.Binding, authScope: cfg.AuthScope}
	chunks, jobs := map[string]bool{}, map[string]bool{}
	for i, input := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if jobs[input.SourceJobID] || i > 0 && input.DocumentBatch.ArtifactId == ordered[i-1].DocumentBatch.ArtifactId {
			return nil, errors.New("duplicate source job or artifact in initial inventory")
		}
		jobs[input.SourceJobID] = true
		source := new(pb.DocumentBatch)
		if err := loader.read(ctx, input.DocumentBatch, source); err != nil {
			return nil, err
		}
		if !proto.Equal(source.Context.GetSnapshotRef(), cfg.Snapshot) || source.Context.GetAuthScopeRef() != cfg.AuthScope || len(source.Chunks) == 0 {
			return nil, errors.New("source snapshot/scope mismatch or empty chunk inventory")
		}
		if _, err := domain.NewIndexSourceView(source, 1_000_000); err != nil {
			return nil, err
		}
		if err := authority.VerifyIndexSourceCheckpoint(ctx, cfg.Snapshot.CorpusId, input.SourceJobID, input.DocumentBatch); err != nil {
			return nil, err
		}
		sort.Slice(source.Chunks, func(i, j int) bool { return source.Chunks[i].Meta.RecordId < source.Chunks[j].Meta.RecordId })
		for start := 0; start < len(source.Chunks); start += cfg.ChunksPerBatch {
			if len(result.plans) == 256 {
				return nil, errors.New("initial inventory exceeds 256 INDEX plans")
			}
			plan := &pb.IndexBuildPlan{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.Snapshot.CorpusId}, TargetSnapshot: cfg.Snapshot, SourceSnapshot: cfg.Snapshot,
				DocumentBatch: input.DocumentBatch, Generation: cfg.Binding.Generation, DictionaryChain: chain, Producer: cfg.Producer, LexicalInputPolicy: "structure-labels-v1"}
			for _, chunk := range source.Chunks[start:min(start+cfg.ChunksPerBatch, len(source.Chunks))] {
				id := chunk.Meta.RecordId
				if chunks[id] {
					return nil, errors.New("chunk occurs in multiple initial sources")
				}
				chunks[id] = true
				plan.Items = append(plan.Items, &pb.IndexBuildItem{ChunkId: id, RecordId: initialPlanID("index-record-v1", cfg.Snapshot.CorpusId, cfg.Binding.Generation.Meta.RecordId, id)})
			}
			// Deterministic protobuf is a version-local byte identity, not a
			// cross-language semantic fingerprint. Marshal before assigning IDs.
			raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(plan)
			if err != nil {
				return nil, err
			}
			identity := initialPlanID("index-plan-v1", cfg.Binding.PublicationID, fmt.Sprint(cfg.Binding.Fence), input.SourceJobID, string(raw))
			plan.Meta.RecordId, plan.OutputBatchId = identity, "index-batch:"+identity
			if err := domain.ValidateIndexBuildPlan(plan); err != nil {
				return nil, err
			}
			ref, _, err := initialPlanArtifact(plan)
			if err != nil {
				return nil, err
			}
			result.plans = append(result.plans, PlannedInitialBatch{input.SourceJobID, plan, ref})
		}
	}
	stats, _, err := loadInitialLexical(ctx, loader, result.plans[0].Plan)
	if err != nil {
		return nil, err
	}
	if stats.DocumentCount != uint64(len(chunks)) {
		return nil, errors.New("frozen BM25 population differs from full source inventory")
	}
	return result, nil
}

func initialPlanID(namespace string, fields ...string) string {
	h := sha256.New()
	h.Write([]byte("regulagraph-" + namespace + "\x00"))
	for _, field := range fields {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		h.Write(size[:])
		h.Write([]byte(field))
	}
	return fmt.Sprintf("%s:%x", namespace, h.Sum(nil))
}

func initialPlanArtifact(plan *pb.IndexBuildPlan) (*pb.ArtifactRef, []byte, error) {
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(plan)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > domain.DefaultWireLimits.MaxBytes {
		return nil, nil, errors.New("INDEX plan exceeds artifact byte limit")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	ref := &pb.ArtifactRef{ArtifactId: plan.Meta.RecordId, SchemaVersion: 1, MediaType: domain.IndexBuildPlanMediaType,
		ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: digest}, StorageKey: "sha256/" + digest[:2] + "/" + digest[2:4] + "/" + digest + ".bin"}
	return ref, raw, domain.ValidateWire(ref, domain.DefaultWireLimits)
}
