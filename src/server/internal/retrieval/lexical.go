// Mengambil kandidat bukti berdasarkan pencocokan lexical BM25.
//
// Peran dalam komponen:
// Menyediakan jalur pencarian kata dan identitas regulasi untuk fusion.
//
// Kontrak integrasi dan perhatian implementasi:
// Gunakan metadata/snapshot yang sama dengan retriever lain; pertanyaan asli tetap dapat digunakan bila normalisasi merusak nomor.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: branch BM25 memakai encoder generation terpin dan backend sparse aktif.
// Factory memeriksa artefak statistik/dictionary; caller membuktikan publication dan read lease.
// Integrasi berikutnya:
// Implement BM25 query path using the pinned analyzer/statistics generation; keep learned sparse as a distinct representation.
// Bukti verifikasi: Test exact legal identifiers, typo/code-switch strata and empty queries; evaluate recall and latency without merging score scales.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package retrieval

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/query"
)

// LexicalRetriever binds a preloaded encoder to the exact generation whose
// verified artifacts produced it. The factory caller authenticates those inputs;
// query requests cannot substitute a different dictionary/statistics generation.
type LexicalRetriever struct {
	encoder    *query.PinnedBM25QueryEncoder
	generation *pb.IndexGeneration
	population *pb.SnapshotRef
	index      SearchIndex
}

func NewLexicalRetriever(encoder *query.PinnedBM25QueryEncoder, generation *pb.IndexGeneration, index SearchIndex) (*LexicalRetriever, error) {
	if encoder == nil || generation == nil || index == nil || !proto.Equal(generation, index.Binding().Generation) {
		return nil, errors.New("lexical encoder and matching trusted generation required")
	}
	bound, population := encoder.ArtifactBinding()
	if bound == nil || population == nil || !proto.Equal(bound, generation) {
		return nil, errors.New("encoder requires verified artifacts from this generation")
	}
	return &LexicalRetriever{encoder: encoder, generation: proto.Clone(generation).(*pb.IndexGeneration), population: population, index: index}, nil
}

// LoadLexicalRetriever authenticates analyzer/dictionary/statistics bytes once
// before constructing the reusable branch. Publication/auth remain caller-owned.
func LoadLexicalRetriever(ctx context.Context, reader query.ArtifactReader, generation *pb.IndexGeneration, index SearchIndex,
	parent, statisticsBase *domain.CheckedLexicalDictionary, maximumBytes uint64, limits domain.WireLimits) (*LexicalRetriever, error) {
	if index == nil || generation == nil || !proto.Equal(index.Binding().Generation, generation) || index.Binding().CorpusID != generation.GetMeta().GetCorpusId() {
		return nil, errors.New("index binding does not match lexical generation")
	}
	encoder, err := query.LoadArtifactBM25Encoder(ctx, reader, generation, parent, statisticsBase, maximumBytes, limits)
	if err != nil {
		return nil, err
	}
	return NewLexicalRetriever(encoder, generation, index)
}

// Retrieve returns a legitimate empty branch for all-OOV input, with OOV count
// preserved. Encoder/backend failure is an error and never an empty success.
func (retriever *LexicalRetriever) Retrieve(ctx context.Context, input SearchInput) (*BranchOutput, error) {
	started := time.Now()
	if retriever == nil || !proto.Equal(input.Generation, retriever.generation) {
		return nil, errors.New("lexical generation mismatch")
	}
	call, cancel, err := validateSearchInput(ctx, input, retriever.index)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if population := retriever.population; population != nil &&
		(input.Scope.SnapshotSeq < population.Sequence || input.Scope.SnapshotSeq == population.Sequence &&
			!proto.Equal(input.Context.SnapshotRef, population)) {
		return nil, errors.New("query snapshot predates or conflicts with frozen statistics population")
	}
	vector, err := retriever.encoder.Encode(input.Question)
	if err != nil {
		return nil, err
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	if len(vector.Indices) == 0 {
		return &BranchOutput{Ranking: RankedBranch{Kind: pb.RetrieverKind_RETRIEVER_KIND_BM25}, OOV: vector.OOV, Duration: time.Since(started)}, nil
	}
	hits, err := retriever.index.SearchSparse(call, &pb.SparseVector{Indices: vector.Indices, Values: vector.Values}, input.Scope)
	if err != nil {
		return nil, fmt.Errorf("BM25 search: %w", err)
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	result, err := branchOutput(input, hits, pb.RetrieverKind_RETRIEVER_KIND_BM25, "bm25", started)
	if err != nil {
		return nil, err
	}
	result.OOV = vector.OOV
	return result, nil
}
