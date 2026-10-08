// Tests bounded evidence reranking against synthetic scores: complete coverage,
// stable ties across pages, model/context ownership, preflight failures and
// cancellation. Scores are fixtures, not relevance or latency acceptance data.
package retrieval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type rerankerFunc func(context.Context, *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error)

func (f rerankerFunc) RerankBatch(c context.Context, r *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
	return f(c, r)
}

func evidenceRerankFixture() (*pb.RequestContext, *pb.EvidenceBundle, EvidenceRerankConfig) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	snapshot := &pb.SnapshotRef{CorpusId: "corpus:one", SnapshotId: "snapshot:one", Sequence: 1, ManifestHash: hash, RepresentationGeneration: "generation:one"}
	call := &pb.RequestContext{SchemaVersion: 1, RequestId: "request:one", TraceId: "trace:one", CorpusId: snapshot.CorpusId, SnapshotRef: snapshot, Deadline: timestamppb.New(time.Now().Add(time.Minute)), ConfigFingerprint: hash, AuthScopeRef: "scope:one"}
	_, _, _, model := rerankFixture()
	bundle := &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: snapshot.CorpusId, RecordId: "bundle:one"}, Snapshot: snapshot, Completeness: pb.Completeness_COMPLETENESS_PARTIAL, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
		MissingDependencies: []string{"parent:missing"}, RetrievalManifest: &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: hash}}
	for i := range 3 {
		id := fmt.Sprintf("evidence:%d", i)
		bundle.Items = append(bundle.Items, &pb.Evidence{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: snapshot.CorpusId, RecordId: id}, Text: fmt.Sprintf("Pasal %d wajib izin.", i), SnapshotRef: snapshot, LegalStatus: pb.LegalStatus_LEGAL_STATUS_UNKNOWN, ParentRefs: []string{"parent:missing"},
			SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "blob:one", RegulationId: "regulation:one", ProvisionVersionId: "version:one"}}, SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:one", EndByte: 20}}, CandidateProvenance: []*pb.Candidate{{EvidenceKey: id, Retriever: pb.RetrieverKind_RETRIEVER_KIND_DENSE, RawScore: float64(3 - i), Rank: uint32(i + 1), Representation: "generation:one"}}})
	}
	return call, bundle, EvidenceRerankConfig{Model: model, MaximumCandidates: 10, PairsPerBatch: 2, MaximumRequestBytes: 4 << 20}
}
func rerankScores(req *pb.RerankBatchRequest) *pb.RerankBatchResponse {
	response := &pb.RerankBatchResponse{RequestId: req.Context.RequestId, Model: proto.Clone(req.Model).(*pb.ModelManifest)}
	for _, pair := range req.Pairs {
		score := 0.5
		if pair.PairId == "evidence:2" {
			score = 0.9
		}
		response.Results = append([]*pb.RerankItemResult{{PairId: pair.PairId, Result: &pb.RerankItemResult_Score{Score: &pb.RerankScore{Score: score, InputTokens: 12, Truncation: &pb.TruncationInfo{OriginalTokens: 12, RetainedTokens: 12}}}}}, response.Results...)
	}
	return response
}
func TestEvidenceRerankingPreservesAllEvidenceAcrossBatches(t *testing.T) {
	call, bundle, cfg := evidenceRerankFixture()
	original := proto.Clone(bundle)
	calls := 0
	keys := map[string]bool{}
	client := rerankerFunc(func(ctx context.Context, req *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
		calls++
		if len(req.Pairs) > 2 || keys[req.OperationKey] {
			t.Fatal("bad batching/identity")
		}
		keys[req.OperationKey] = true
		if !proto.Equal(req.Context, call) || req.Pairs[0].Query != "izin?" || len(req.Pairs[0].Provenance.Sources) != 1 {
			t.Fatal("context/provenance drift")
		}
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(call.Deadline.AsTime()) {
			t.Fatal("unbounded model call")
		}
		return rerankScores(req), nil
	})
	r, err := NewEvidenceReranker(client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Version = "mutated"
	result, err := r.Rank(context.Background(), call, "izin?", bundle)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.Batches != 2 || len(result.Scores) != 3 {
		t.Fatal("incomplete ranking")
	}
	for i, id := range []string{"evidence:2", "evidence:0", "evidence:1"} {
		if result.Evidence.Items[i].Meta.RecordId != id || result.Scores[i].PairId != id {
			t.Fatal("unstable ranking/ties")
		}
		if result.Scores[i].ModelManifest.Version == "mutated" {
			t.Fatal("config was not frozen")
		}
	}
	if result.Evidence.Completeness != bundle.Completeness || len(result.Evidence.MissingDependencies) != 1 || !proto.Equal(bundle, original) {
		t.Fatal("reranker changed completeness or input")
	}
	result.Evidence.Items[0].Text = "changed"
	if !proto.Equal(bundle, original) {
		t.Fatal("output aliases input")
	}
}
func TestEvidenceRerankingRejectsBeforeInference(t *testing.T) {
	for _, name := range []string{"scope snapshot", "foreign item", "duplicate", "budget", "oversized pair", "deadline", "cancel", "model mismatch"} {
		t.Run(name, func(t *testing.T) {
			call, bundle, cfg := evidenceRerankFixture()
			ctx := context.Background()
			switch name {
			case "scope snapshot":
				call.SnapshotRef = proto.Clone(call.SnapshotRef).(*pb.SnapshotRef)
				call.SnapshotRef.Sequence++
			case "foreign item":
				bundle.Items[0].Meta.CorpusId = "other"
			case "duplicate":
				bundle.Items[1] = bundle.Items[0]
			case "budget":
				cfg.MaximumCandidates = 2
			case "oversized pair":
				cfg.MaximumRequestBytes = 1500
				bundle.Items[2].Text = strings.Repeat("x", 2000)
			case "deadline":
				call.Deadline = timestamppb.New(time.Now().Add(-time.Second))
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "model mismatch":
				bundle.RetrievalManifest.Models = []*pb.ModelManifest{proto.Clone(cfg.Model).(*pb.ModelManifest)}
				bundle.RetrievalManifest.Models[0].Version = "other"
			}
			calls := 0
			r, err := NewEvidenceReranker(rerankerFunc(func(context.Context, *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
				calls++
				return nil, nil
			}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if out, err := r.Rank(ctx, call, "izin?", bundle); err == nil || out != nil || calls != 0 {
				t.Fatal("invalid input sent to model", err, calls)
			}
		})
	}
}
func TestEvidenceRerankingRejectsIncompleteOrTruncatedResponse(t *testing.T) {
	for _, name := range []string{"missing", "duplicate", "truncated", "model", "failure", "cancel"} {
		t.Run(name, func(t *testing.T) {
			call, bundle, cfg := evidenceRerankFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r, err := NewEvidenceReranker(rerankerFunc(func(_ context.Context, req *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
				res := rerankScores(req)
				switch name {
				case "missing":
					res.Results = res.Results[:1]
				case "duplicate":
					res.Results[1] = res.Results[0]
				case "truncated":
					res.Results[0].GetScore().Truncation = &pb.TruncationInfo{OriginalTokens: 20, RetainedTokens: 12, Truncated: true}
				case "model":
					res.Model.Version = "other"
				case "failure":
					return nil, errors.New("native unavailable")
				case "cancel":
					cancel()
				}
				return res, nil
			}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if out, err := r.Rank(ctx, call, "izin?", bundle); err == nil || out != nil {
				t.Fatal("invalid model response became evidence")
			}
		})
	}
}
func TestEvidenceRerankingEmptyCallsNoModel(t *testing.T) {
	call, bundle, cfg := evidenceRerankFixture()
	bundle.Items = nil
	r, err := NewEvidenceReranker(rerankerFunc(func(context.Context, *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
		t.Fatal("empty evidence called model")
		return nil, nil
	}), cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Rank(context.Background(), call, "izin?", bundle)
	if err != nil || result.Batches != 0 || len(result.Scores) != 0 {
		t.Fatal("empty accounting", err)
	}
}

func TestEvidenceRerankingSplitsByWireBytes(t *testing.T) {
	call, bundle, cfg := evidenceRerankFixture()
	cfg.PairsPerBatch = 128
	cfg.MaximumRequestBytes = 4000
	for _, item := range bundle.Items {
		item.Text = strings.Repeat("a", 2400)
	}
	calls := 0
	r, err := NewEvidenceReranker(rerankerFunc(func(_ context.Context, req *pb.RerankBatchRequest) (*pb.RerankBatchResponse, error) {
		calls++
		if len(req.Pairs) != 1 || proto.Size(req) > cfg.MaximumRequestBytes {
			t.Fatal("wire byte budget did not partition requests")
		}
		return rerankScores(req), nil
	}), cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Rank(context.Background(), call, "izin?", bundle)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || out.Batches != 3 || len(out.Scores) != 3 || out.Scores[0].PairId != "evidence:2" || out.Scores[1].PairId != "evidence:0" || out.Scores[2].PairId != "evidence:1" {
		t.Fatal("byte partition lost ranking or tie order")
	}
}
