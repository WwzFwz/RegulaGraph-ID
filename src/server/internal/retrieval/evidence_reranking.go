// Reranks authenticated, snapshot-bound evidence in bounded native batches.
// The hydrator owns source integrity and legal filtering. This stage preserves
// every evidence item, dependency/path and original branch provenance; only order
// changes. Partial/error/truncated model output fails explicitly, with no silent
// fallback or top-k pruning. Models/clients are initialized once outside requests.
// Report before/after nDCG, evidence coverage, model/tokenizer, pair counts and
// queue-inclusive p95/p99 under configs/benchmark-targets.yaml. Ranking scores
// are not answer confidence; production quality/performance remain unmeasured.
package retrieval

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type RerankingClient interface {
	RerankBatch(context.Context, *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error)
}

type EvidenceRerankConfig struct {
	Model               *pb.ModelManifest
	MaximumCandidates   int
	PairsPerBatch       int
	MaximumRequestBytes int
}

type EvidenceReranker struct {
	client RerankingClient
	config EvidenceRerankConfig
}

type EvidenceRerankResult struct {
	Model    *pb.ModelManifest
	Evidence *pb.EvidenceBundle
	Scores   []*pb.RerankResult // In the same order as Evidence.Items; PairId is evidence ID.
	Batches  int
	Duration time.Duration
}

func NewEvidenceReranker(client RerankingClient, config EvidenceRerankConfig) (*EvidenceReranker, error) {
	if client == nil || config.MaximumCandidates < 1 || config.MaximumCandidates > 256 || config.PairsPerBatch < 1 || config.PairsPerBatch > 128 || config.MaximumRequestBytes < 1 || config.MaximumRequestBytes > 4<<20 {
		return nil, errors.New("bounded reranker client/config required")
	}
	if err := domain.ValidateWire(config.Model, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if config.Model.Task != pb.ModelTask_MODEL_TASK_RERANK {
		return nil, errors.New("RERANK model required")
	}
	config.Model = proto.Clone(config.Model).(*pb.ModelManifest)
	return &EvidenceReranker{client: client, config: config}, nil
}

// Rank must only receive evidence from an authorized hydrator, never client JSON.
// All request pages are preflighted before inference. Ties preserve input order
// across pages, regardless of worker response order. Empty evidence calls no model.
func (r *EvidenceReranker) Rank(ctx context.Context, call *pb.RequestContext, question string, bundle *pb.EvidenceBundle) (*EvidenceRerankResult, error) {
	started := time.Now()
	if ctx == nil || r == nil || r.client == nil || question == "" || len(question) > 64<<10 || !utf8.ValidString(question) {
		return nil, errors.New("reranker context and bounded question required")
	}
	for _, m := range []proto.Message{call, bundle} {
		if err := domain.ValidateWire(m, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	if call.SnapshotRef == nil || call.CorpusId != bundle.Meta.CorpusId || !proto.Equal(call.SnapshotRef, bundle.Snapshot) || len(bundle.Items) > r.config.MaximumCandidates ||
		bundle.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return nil, errors.New("rerank evidence snapshot, status or budget mismatch")
	}
	bounded, cancel := context.WithDeadline(ctx, call.Deadline.AsTime())
	defer cancel()
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	owned := proto.Clone(bundle).(*pb.EvidenceBundle)
	modelRecorded := false
	for _, model := range owned.RetrievalManifest.Models {
		if model.Task == pb.ModelTask_MODEL_TASK_RERANK {
			if !proto.Equal(model, r.config.Model) {
				return nil, errors.New("retrieval manifest pins a different reranker")
			}
			modelRecorded = true
		}
	}
	if !modelRecorded {
		owned.RetrievalManifest.Models = append(owned.RetrievalManifest.Models, proto.Clone(r.config.Model).(*pb.ModelManifest))
	}
	call = proto.Clone(call).(*pb.RequestContext)
	seen := map[string]bool{}
	for _, item := range owned.Items {
		if item.Meta.CorpusId != call.CorpusId || !proto.Equal(item.SnapshotRef, call.SnapshotRef) || seen[item.Meta.RecordId] {
			return nil, errors.New("duplicate or foreign rerank evidence")
		}
		seen[item.Meta.RecordId] = true
		for _, candidate := range item.CandidateProvenance {
			if candidate.EvidenceKey != item.Meta.RecordId {
				return nil, errors.New("rerank provenance belongs to another evidence")
			}
		}
	}
	type page struct {
		request *pb.RerankBatchRequest
		start   int
	}
	pages := []page{}
	newRequest := func() *pb.RerankBatchRequest {
		return &pb.RerankBatchRequest{Context: proto.Clone(call).(*pb.RequestContext), Model: proto.Clone(r.config.Model).(*pb.ModelManifest), OperationKey: "rerank:" + strings.Repeat("0", 64)}
	}
	request := newRequest()
	size := proto.Size(request)
	start := 0
	for i, item := range owned.Items {
		pair := &pb.RerankPair{PairId: item.Meta.RecordId, Query: question, Text: item.Text, Provenance: &pb.Provenance{Sources: item.SourceRefs, Spans: item.SourceSpans, Locators: item.Locators}}
		cost := protowire.SizeTag(3) + protowire.SizeBytes(proto.Size(pair))
		if len(request.Pairs) > 0 && (len(request.Pairs) == r.config.PairsPerBatch || size+cost > r.config.MaximumRequestBytes) {
			pages = append(pages, page{request, start})
			request = newRequest()
			size = proto.Size(request)
			start = i
		}
		if size+cost > r.config.MaximumRequestBytes {
			return nil, errors.New("single rerank pair exceeds request byte budget")
		}
		request.Pairs = append(request.Pairs, pair)
		size += cost
	}
	if len(request.Pairs) > 0 {
		pages = append(pages, page{request, start})
	}
	for _, page := range pages {
		// Hash exact request bytes with a fixed operation-key placeholder to bind
		// query, evidence, model, scope and snapshot without exposing source text.
		raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(page.request)
		if err != nil {
			return nil, err
		}
		page.request.OperationKey = fmt.Sprintf("rerank:%x", sha256.Sum256(raw))
		if err = domain.ValidateWire(page.request, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if proto.Size(page.request) > r.config.MaximumRequestBytes {
			return nil, errors.New("rerank request exceeds transport budget")
		}
	}
	type ranked struct {
		item  *pb.Evidence
		score *pb.RerankResult
	}
	ordered := make([]ranked, 0, len(owned.Items))
	for _, page := range pages {
		if err := bounded.Err(); err != nil {
			return nil, err
		}
		response, err := r.client.RerankBatch(bounded, proto.Clone(page.request).(*pb.RerankBatchRequest))
		if err != nil {
			return nil, err
		}
		if err = bounded.Err(); err != nil {
			return nil, err
		}
		candidates := make([]FusedCandidate, len(page.request.Pairs))
		ids := make([]string, len(candidates))
		for i, pair := range page.request.Pairs {
			item := owned.Items[page.start+i]
			ids[i] = pair.PairId
			candidates[i] = FusedCandidate{EvidenceKey: item.Meta.RecordId, Provenance: item.CandidateProvenance}
		}
		correlated, err := CorrelateRerankBatch(candidates, ids, response, call.RequestId, r.config.Model, r.config.PairsPerBatch)
		if err != nil {
			return nil, err
		}
		scores := map[string]RerankedCandidate{}
		for _, value := range correlated {
			if value.Truncation.Truncated {
				return nil, errors.New("reranker truncated authenticated evidence pair")
			}
			scores[value.Candidate.EvidenceKey] = value
		}
		for i, id := range ids {
			value := scores[id]
			ordered = append(ordered, ranked{owned.Items[page.start+i], &pb.RerankResult{PairId: id, Score: value.Score, ModelManifest: proto.Clone(r.config.Model).(*pb.ModelManifest), InputTokens: uint64(value.InputTokens), Truncation: value.Truncation}})
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].score.Score > ordered[j].score.Score })
	result := &EvidenceRerankResult{Model: proto.Clone(r.config.Model).(*pb.ModelManifest), Evidence: owned, Batches: len(pages), Duration: time.Since(started)}
	for i, item := range ordered {
		owned.Items[i] = item.item
		result.Scores = append(result.Scores, item.score)
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
