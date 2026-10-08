// Authenticates a bounded, complete first-snapshot INDEX write set before any
// backend mutation. Plans and source bytes are hash-pinned; successful CHUNK
// checkpoints and dictionary mappings come from PostgreSQL authority. This
// accepts no parent snapshot, incremental closures or partial source selection.
// Worker DocumentBatch/IndexBatch addresses are independent of logical record
// IDs; plan and lexical references retain their coordinator-owned typed IDs.
// Exact registered refs, media types, hashes and planned output IDs remain gates.
// Serialized artifact bytes are capped at 64 MiB per prepared write; decoded
// messages add memory. Measure RSS/time under configs/benchmark-targets.yaml.
// Quality and required performance remain unmeasured; no model is called here.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type IndexAuthority interface {
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	VerifyIndexSourceCheckpoint(context.Context, string, string, *pb.ArtifactRef) error
	VerifyIndexDictionary(context.Context, string, *pb.LexicalDictionaryArtifact) error
}
type IndexArtifactReader interface {
	ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error)
}
type InitialIndexInput struct {
	SourceJobID string
	BatchRef    *pb.ArtifactRef
}

// PreparedInitialIndex owns decoded records and a content-bound write identity.
// Callers cannot construct a usable value without admission. It is not approval
// to publish other backends; the publication coordinator still requires receipts.
type PreparedInitialIndex struct {
	binding  domain.IndexCatalogBinding
	snapshot *pb.SnapshotRef
	records  []*pb.IndexRecord
	digest   string
}

func (p *PreparedInitialIndex) ExpectedBackend() *pb.BackendGeneration {
	if p == nil || p.snapshot == nil {
		return nil
	}
	n := uint64(len(p.records))
	return &pb.BackendGeneration{Backend: pb.BackendKind_BACKEND_KIND_QDRANT,
		Generation:         p.binding.Generation.Meta.RecordId,
		OperationsChecksum: &pb.ContentHash{Sha256: p.digest}, ExpectedCounts: &pb.Counts{Expected: n, Accepted: n}}
}

func PrepareInitialIndex(ctx context.Context, authority IndexAuthority, reader IndexArtifactReader,
	binding domain.IndexCatalogBinding, inputs []InitialIndexInput) (*PreparedInitialIndex, error) {
	return prepareInitialIndex(ctx, authority, reader, binding, inputs, nil)
}

func prepareInitialIndex(ctx context.Context, authority IndexAuthority, reader IndexArtifactReader,
	binding domain.IndexCatalogBinding, inputs []InitialIndexInput, loader *initialArtifactLoader) (*PreparedInitialIndex, error) {
	if authority == nil || reader == nil || len(inputs) == 0 || len(inputs) > 256 {
		return nil, errors.New("bounded initial INDEX inputs required")
	}
	if err := domain.ValidateIndexCatalogBinding(binding); err != nil {
		return nil, err
	}
	binding.Generation = proto.Clone(binding.Generation).(*pb.IndexGeneration)
	ordered := append([]InitialIndexInput(nil), inputs...)
	for i := range ordered {
		if ordered[i].BatchRef == nil || ordered[i].SourceJobID == "" {
			return nil, errors.New("source job and batch reference required")
		}
		ordered[i].BatchRef = proto.Clone(ordered[i].BatchRef).(*pb.ArtifactRef)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].BatchRef.ArtifactId < ordered[j].BatchRef.ArtifactId })
	if loader == nil {
		loader = &initialArtifactLoader{authority: authority, reader: reader, corpus: binding.Generation.Meta.CorpusId, remaining: 64 << 20, cache: map[string]initialArtifact{}}
	}
	p := &PreparedInitialIndex{binding: binding}
	type sourceCoverage struct {
		job    string
		source *pb.DocumentBatch
		chunks map[string]bool
	}
	sources := map[string]*sourceCoverage{}
	ids, chunks := map[string]bool{}, map[string]bool{}
	var firstPlan *pb.IndexBuildPlan
	for i, input := range ordered {
		if i > 0 && input.BatchRef.ArtifactId == ordered[i-1].BatchRef.ArtifactId {
			return nil, errors.New("duplicate initial INDEX artifact")
		}
		if input.BatchRef.MediaType != domain.IndexBatchMediaType {
			return nil, errors.New("typed INDEX batch required")
		}
		batch, plan, source := new(pb.IndexBatch), new(pb.IndexBuildPlan), new(pb.DocumentBatch)
		if err := loader.read(ctx, input.BatchRef, batch); err != nil {
			return nil, err
		}
		if err := loader.read(ctx, batch.BuildPlan, plan); err != nil {
			return nil, err
		}
		if err := loader.read(ctx, plan.DocumentBatch, source); err != nil {
			return nil, err
		}
		if err := domain.ValidatePlannedIndexBatch(batch, plan, batch.BuildPlan, source); err != nil {
			return nil, err
		}
		if len(batch.Closures) != 0 || len(batch.Records) == 0 || !proto.Equal(batch.Generation, binding.Generation) {
			return nil, errors.New("initial writer requires nonempty upserts in one generation, without closures")
		}
		if firstPlan == nil {
			firstPlan = plan
			p.snapshot = proto.Clone(plan.TargetSnapshot).(*pb.SnapshotRef)
			if p.snapshot.Sequence > 1<<53-1 {
				return nil, errors.New("initial snapshot exceeds exact Qdrant filter range")
			}
		}
		if !proto.Equal(plan.TargetSnapshot, p.snapshot) || !proto.Equal(plan.SourceSnapshot, p.snapshot) {
			return nil, errors.New("initial sources must belong to the exact target snapshot")
		}
		if len(plan.DictionaryChain) != len(firstPlan.DictionaryChain) {
			return nil, errors.New("dictionary chain drift")
		}
		for j, ref := range plan.DictionaryChain {
			if !proto.Equal(ref, firstPlan.DictionaryChain[j]) {
				return nil, errors.New("dictionary chain drift")
			}
		}
		key := plan.DocumentBatch.ArtifactId
		coverage := sources[key]
		if coverage == nil {
			if err := authority.VerifyIndexSourceCheckpoint(ctx, loader.corpus, input.SourceJobID, plan.DocumentBatch); err != nil {
				return nil, err
			}
			coverage = &sourceCoverage{job: input.SourceJobID, source: source, chunks: map[string]bool{}}
			sources[key] = coverage
		} else if coverage.job != input.SourceJobID {
			return nil, errors.New("source checkpoint ownership changed")
		}
		for _, record := range batch.Records {
			if ids[record.Meta.RecordId] || chunks[record.ChunkId] {
				return nil, errors.New("duplicate index record/chunk across batches")
			}
			ids[record.Meta.RecordId], chunks[record.ChunkId], coverage.chunks[record.ChunkId] = true, true, true
			p.records = append(p.records, record)
		}
	}
	for _, coverage := range sources {
		if len(coverage.chunks) != len(coverage.source.Chunks) {
			return nil, errors.New("initial write omits source chunks")
		}
		for _, chunk := range coverage.source.Chunks {
			if !coverage.chunks[chunk.Meta.RecordId] {
				return nil, errors.New("initial write omits source chunk")
			}
		}
	}
	if err := verifyInitialLexical(ctx, loader, firstPlan, p.records); err != nil {
		return nil, err
	}
	// Versioned length-prefixed UTF-8 fields bind the complete artifact byte set,
	// job authority and physical target. This is not a protobuf semantic hash.
	h := sha256.New()
	h.Write([]byte("regulagraph-initial-index-write-v1\x00"))
	field := func(v string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(v)))
		h.Write(size[:])
		h.Write([]byte(v))
	}
	for _, v := range []string{binding.PublicationID, fmt.Sprint(binding.Fence), binding.Endpoint, binding.Collection, loader.corpus, binding.Generation.Meta.RecordId} {
		field(v)
	}
	for _, input := range ordered {
		field(input.SourceJobID)
		field(input.BatchRef.ArtifactId)
		field(input.BatchRef.ContentHash.Sha256)
	}
	p.digest = hex.EncodeToString(h.Sum(nil))
	return p, nil
}

type initialArtifact struct {
	ref *pb.ArtifactRef
	raw []byte
}
type initialArtifactLoader struct {
	authority IndexAuthority
	reader    IndexArtifactReader
	corpus    string
	remaining uint64
	cache     map[string]initialArtifact
}

func (l *initialArtifactLoader) read(ctx context.Context, ref *pb.ArtifactRef, target proto.Message) error {
	if ref == nil {
		return errors.Join(errors.New("missing initial INDEX artifact reference"), domain.ErrPersistentIntegrity)
	}
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return errors.Join(err, domain.ErrPersistentIntegrity)
	}
	_, documentBatch := target.(*pb.DocumentBatch)
	mediaMatches := ref.MediaType == "application/x-protobuf; message="+string(target.ProtoReflect().Descriptor().FullName())
	if !mediaMatches && !(documentBatch && domain.IsDocumentBatchMediaType(ref.MediaType)) {
		return errors.Join(errors.New("initial INDEX artifact media type mismatch"), domain.ErrPersistentIntegrity)
	}
	entry, ok := l.cache[ref.ArtifactId]
	if ok && !proto.Equal(entry.ref, ref) {
		return errors.Join(errors.New("artifact reference drift"), domain.ErrPersistentIntegrity)
	}
	if !ok {
		if ref.ByteSize == 0 || ref.ByteSize > 16<<20 || ref.ByteSize > l.remaining {
			return errors.Join(errors.New("initial INDEX artifact budget exceeded"), domain.ErrPersistentIntegrity)
		}
		registered, err := l.authority.LoadArtifact(ctx, l.corpus, ref.ArtifactId)
		if err != nil {
			return indexArtifactReadError(err)
		}
		if !proto.Equal(registered, ref) {
			return errors.Join(errors.New("unregistered initial INDEX artifact"), domain.ErrPersistentIntegrity)
		}
		raw, err := l.reader.ReadVerified(ctx, ref, ref.ByteSize)
		if err != nil {
			return indexArtifactReadError(err)
		}
		sum := sha256.Sum256(raw)
		if uint64(len(raw)) != ref.ByteSize || hex.EncodeToString(sum[:]) != ref.ContentHash.Sha256 {
			return errors.Join(errors.New("initial INDEX artifact hash/size mismatch"), domain.ErrPersistentIntegrity)
		}
		l.remaining -= uint64(len(raw))
		entry = initialArtifact{proto.Clone(ref).(*pb.ArtifactRef), raw}
		l.cache[ref.ArtifactId] = entry
	}
	limits := domain.DefaultWireLimits
	limits.MaxItems = 1_000_000
	if err := domain.DecodeWire(entry.raw, target, limits); err != nil {
		return errors.Join(err, domain.ErrPersistentIntegrity)
	}
	message := target.ProtoReflect()
	metaField := message.Descriptor().Fields().ByName("meta")
	if metaField == nil {
		return errors.Join(errors.New("artifact lacks record metadata"), domain.ErrPersistentIntegrity)
	}
	meta, ok := message.Get(metaField).Message().Interface().(*pb.RecordMeta)
	if !ok || meta.CorpusId != l.corpus {
		return errors.Join(errors.New("artifact corpus mismatch"), domain.ErrPersistentIntegrity)
	}
	// Rust stores worker outputs under content-addressed IDs. Their logical IDs
	// are authenticated inside the hashed bytes, with INDEX output identity
	// checked against plan.OutputBatchId by ValidatePlannedIndexBatch. Only these
	// batch types permit separate identities; typed plan/lexical refs stay exact.
	switch target.(type) {
	case *pb.DocumentBatch, *pb.IndexBatch:
	default:
		if meta.RecordId != ref.ArtifactId {
			return errors.Join(errors.New("typed artifact identity mismatch"), domain.ErrPersistentIntegrity)
		}
	}
	return nil
}

// Missing immutable prerequisites require repair/replan; transient database or
// filesystem errors keep their original classification and remain retryable.
func indexArtifactReadError(err error) error {
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, domain.ErrPersistentIntegrity)
	}
	return err
}

func verifyInitialLexical(ctx context.Context, l *initialArtifactLoader, plan *pb.IndexBuildPlan, records []*pb.IndexRecord) error {
	stats, parent, err := loadInitialLexical(ctx, l, plan)
	if err != nil {
		return err
	}
	if stats.DocumentCount != uint64(len(records)) {
		return errors.New("initial BM25 statistics population differs from complete write set")
	}
	return verifyIndexSparseTerms(parent, records)
}

func verifyIndexSparseTerms(parent *domain.CheckedLexicalDictionary, records []*pb.IndexRecord) error {
	terms := map[uint32]bool{}
	for _, id := range parent.Terms() {
		terms[id] = true
	}
	for _, record := range records {
		for _, id := range record.SparseVector.Indices {
			if !terms[id] {
				return errors.New("index sparse term is absent from authoritative dictionary")
			}
		}
	}
	return nil
}

// loadInitialLexical shares generation admission between planning and writing;
// only the caller knows the complete selected population count.
func loadInitialLexical(ctx context.Context, l *initialArtifactLoader, plan *pb.IndexBuildPlan) (*pb.LexicalStatisticsArtifact, *domain.CheckedLexicalDictionary, error) {
	analyzer, stats := new(pb.LexicalAnalyzerArtifact), new(pb.LexicalStatisticsArtifact)
	if err := l.read(ctx, plan.Generation.LexicalAnalyzer, analyzer); err != nil {
		return nil, nil, err
	}
	if err := domain.ValidateLexicalAnalyzerArtifact(analyzer, l.corpus, domain.DefaultWireLimits); err != nil {
		return nil, nil, err
	}
	if err := l.read(ctx, plan.Generation.LexicalStatistics, stats); err != nil {
		return nil, nil, err
	}
	var parent, base *domain.CheckedLexicalDictionary
	for _, ref := range plan.DictionaryChain {
		dictionary := new(pb.LexicalDictionaryArtifact)
		if err := l.read(ctx, ref, dictionary); err != nil {
			return nil, nil, err
		}
		checked, err := domain.CheckLexicalDictionaryArtifact(dictionary, l.corpus, parent, domain.DefaultWireLimits)
		if err != nil {
			return nil, nil, err
		}
		if err = l.authority.VerifyIndexDictionary(ctx, l.corpus, dictionary); err != nil {
			return nil, nil, err
		}
		if checked.RegistryRevision() == stats.DictionaryRegistryRevision {
			base = checked
		}
		parent = checked
	}
	if err := domain.CheckLexicalStatisticsArtifact(stats, base, domain.DefaultWireLimits); err != nil {
		return nil, nil, err
	}
	if stats.InputPolicy != plan.LexicalInputPolicy || !proto.Equal(stats.PopulationSnapshot, plan.TargetSnapshot) {
		return nil, nil, errors.New("initial BM25 statistics population/policy differs from target snapshot")
	}
	return stats, parent, nil
}
