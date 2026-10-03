// Mengambil kandidat bukti melalui embedding pertanyaan dan pencarian vector.
//
// Peran dalam komponen:
// Menyediakan jalur semantik untuk variasi istilah dan bahasa.
//
// Kontrak integrasi dan perhatian implementasi:
// Model query harus kompatibel dengan indeks; cache memasukkan model version dan snapshot/filter yang relevan.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
//
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: query embedding native dan pencarian dense generation-bound aktif sebagai branch.
// Caller wajib memegang read lease dan menghidrasi/filter bukti sebelum answering.
// Integrasi berikutnya:
// Encode query with the index-compatible model and retrieve bounded snapshot/temporal candidates via Qdrant adapter.
// Bukti verifikasi: Measure Recall@k and p95/p99 including embedding queue; test missing generation and representation mismatch.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
)

// EmbeddingClient is implemented by the reusable native adapter. Retrieval also
// checks its response so alternate clients cannot bypass correlation checks.
type EmbeddingClient interface {
	EmbedBatch(context.Context, *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error)
}

// SearchIndex must enforce corpus/generation/visibility on backend hits. Its
// binding originates in the trusted generation catalog, never in query input.
type SearchIndex interface {
	Binding() qdrant.Binding
	SearchDense(context.Context, []float32, qdrant.SearchScope) ([]qdrant.Hit, error)
	SearchSparse(context.Context, *pb.SparseVector, qdrant.SearchScope) ([]qdrant.Hit, error)
}

// BranchOutput is a search result, not hydrated legal evidence. The caller must
// verify hit identity against the snapshot catalog and apply legal-time policy.
type BranchOutput struct {
	Ranking  RankedBranch
	Hits     []qdrant.Hit
	OOV      uint32
	Duration time.Duration
}

type SearchInput struct {
	Context    *pb.RequestContext
	Question   string
	Generation *pb.IndexGeneration
	Scope      qdrant.SearchScope
}

// RetrieveDense embeds exactly one original question with QUERY purpose, rejects
// truncation and model drift, then performs one bounded backend search. It never
// changes a requested snapshot or silently retries an unsuccessful branch.
func RetrieveDense(ctx context.Context, input SearchInput, client EmbeddingClient, index SearchIndex) (*BranchOutput, error) {
	started := time.Now()
	call, cancel, err := validateSearchInput(ctx, input, index)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if client == nil {
		return nil, errors.New("query embedding client required")
	}
	request := &pb.EmbedBatchRequest{Context: proto.Clone(input.Context).(*pb.RequestContext),
		Model:   proto.Clone(input.Generation.DenseManifest).(*pb.ModelManifest),
		Purpose: pb.EmbeddingPurpose_EMBEDDING_PURPOSE_QUERY, OperationKey: input.Context.RequestId,
		Items: []*pb.TextItem{{ItemId: "query", Text: input.Question}}}
	response, err := client.EmbedBatch(call, request)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	if err = domain.VerifyEmbeddingResultsWithLimits(request, response, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if len(response.Results) != 1 || response.Results[0].GetEmbedding() == nil {
		return nil, errors.New("query embedding failed or is missing")
	}
	embedding := response.Results[0].GetEmbedding()
	if embedding.InputTokens == 0 || embedding.InputTokens > request.Model.MaxTokens ||
		embedding.Truncation == nil || embedding.Truncation.Truncated ||
		embedding.Truncation.OriginalTokens != embedding.Truncation.RetainedTokens ||
		embedding.Truncation.RetainedTokens > uint64(request.Model.MaxTokens) {
		return nil, errors.New("query embedding has invalid tokens or truncates the question")
	}
	norm := 0.0
	for _, value := range embedding.Values {
		norm += float64(value) * float64(value)
	}
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) ||
		request.Model.Normalization == "l2" && math.Abs(norm-1) > 0.01 {
		return nil, errors.New("query embedding normalization mismatch")
	}
	hits, err := index.SearchDense(call, embedding.Values, input.Scope)
	if err != nil {
		return nil, fmt.Errorf("dense search: %w", err)
	}
	if err = call.Err(); err != nil {
		return nil, err
	}
	return branchOutput(input, hits, pb.RetrieverKind_RETRIEVER_KIND_DENSE, "dense", started)
}

func validateSearchInput(ctx context.Context, input SearchInput, index SearchIndex) (context.Context, context.CancelFunc, error) {
	if ctx == nil || index == nil || input.Context == nil || input.Context.SnapshotRef == nil ||
		len(input.Question) > 64<<10 || strings.TrimSpace(input.Question) == "" || !utf8.ValidString(input.Question) ||
		input.Scope.Limit < 1 || input.Scope.Limit > 1000 || input.Scope.SnapshotSeq == 0 ||
		input.Scope.SnapshotSeq > (1<<53)-1 {
		return nil, nil, errors.New("bounded question, snapshot and search dependencies required")
	}
	if err := domain.ValidateWire(input.Context, domain.DefaultWireLimits); err != nil {
		return nil, nil, err
	}
	if err := domain.ValidatePairedIndexGeneration(input.Generation); err != nil {
		return nil, nil, err
	}
	if input.Generation.DenseManifest.Task != pb.ModelTask_MODEL_TASK_EMBED ||
		input.Generation.DenseManifest.GetDimensions() == 0 || input.Generation.DenseManifest.GetDimensions() > 16384 {
		return nil, nil, errors.New("search generation requires a bounded embedding model")
	}
	binding := index.Binding()
	if binding.CorpusID != input.Context.CorpusId || input.Generation.Meta.CorpusId != input.Context.CorpusId ||
		!proto.Equal(binding.Generation, input.Generation) || input.Context.SnapshotRef.Sequence != input.Scope.SnapshotSeq ||
		input.Context.SnapshotRef.RepresentationGeneration != input.Generation.Meta.RecordId {
		return nil, nil, errors.New("search corpus, snapshot or generation binding mismatch")
	}
	if input.Scope.ProvisionVersionID != "" {
		if !validSearchID(input.Scope.ProvisionVersionID) {
			return nil, nil, errors.New("invalid requested provision version")
		}
	}
	call, cancel := context.WithDeadline(ctx, input.Context.Deadline.AsTime())
	if err := call.Err(); err != nil {
		cancel()
		return nil, nil, err
	}
	return call, cancel, nil
}

func branchOutput(input SearchInput, hits []qdrant.Hit, kind pb.RetrieverKind, representation string, started time.Time) (*BranchOutput, error) {
	if len(hits) > input.Scope.Limit {
		return nil, errors.New("backend exceeded candidate budget")
	}
	output := &BranchOutput{Ranking: RankedBranch{Kind: kind}, Hits: make([]qdrant.Hit, len(hits)), Duration: time.Since(started)}
	seen := make(map[string]bool, len(hits))
	for i, hit := range hits {
		if seen[hit.RecordID] || !validSearchID(hit.ChunkID) || !validSearchID(hit.PointID) || len(hit.ProvisionVersionIDs) == 0 || len(hit.ProvisionVersionIDs) > 1000 ||
			math.IsNaN(hit.Score) || math.IsInf(hit.Score, 0) || i > 0 && hit.Score > hits[i-1].Score {
			return nil, errors.New("backend returned duplicate, unversioned or misranked hits")
		}
		matched := input.Scope.ProvisionVersionID == ""
		for _, version := range hit.ProvisionVersionIDs {
			if !validSearchID(version) {
				return nil, errors.New("backend returned invalid provision version identity")
			}
			matched = matched || version == input.Scope.ProvisionVersionID
		}
		if !matched {
			return nil, errors.New("backend hit lacks requested version")
		}
		candidate := &pb.Candidate{EvidenceKey: hit.RecordID, Retriever: kind,
			RawScore: hit.Score, Rank: uint32(i + 1), Representation: representation,
			FilterDecisions: []*pb.FilterDecision{{Rule: "index-corpus-generation-snapshot", Accepted: true,
				Reason: "Bound index visibility checked; legal-time and source hydration still required"}}}
		if err := domain.ValidateWire(candidate, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		seen[hit.RecordID] = true
		output.Ranking.Candidates = append(output.Ranking.Candidates, candidate)
		output.Hits[i] = hit
		output.Hits[i].ProvisionVersionIDs = append([]string(nil), hit.ProvisionVersionIDs...)
	}
	return output, nil
}

func validSearchID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
