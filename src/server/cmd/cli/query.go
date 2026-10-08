// Runs an operator-scoped evidence query through the production pinned workflow.
// Inputs select question/date/profile; storage routing comes from the published
// catalog and must match an explicitly allowed Qdrant origin. Secrets stay in
// environment variables and backend errors are not dumped to stdout/stderr.
// This command returns evidence, never an LLM answer or quality PASS. Reuse the
// native process; CLI cold setup is included in wall time and measured separately
// from warm latency under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/workflows"
)

type queryOptions struct {
	Corpus, Question, AsOf, Profile, Unresolved, Snapshot, Build string
	Limit                                                        int
	Timeout                                                      time.Duration
	dsn, artifacts, native, qdrant, key                          string
	rerankManifest, rerankHash                                   string
}

func runQueryEvidence(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runQueryEvidenceWith(ctx, args, out, errOut, os.Getenv, executeEvidenceQuery)
}

func runQueryEvidenceWith(ctx context.Context, args []string, out, errOut io.Writer, env func(string) string, execute func(context.Context, queryOptions, *pb.QuestionRequest) (*workflows.RAGResult, error)) int {
	fs := flag.NewFlagSet("query-evidence", flag.ContinueOnError)
	fs.SetOutput(errOut)
	opts := queryOptions{}
	fs.StringVar(&opts.Question, "question", "", "Regulatory question (UTF-8)")
	fs.StringVar(&opts.AsOf, "as-of", "", "Required legal date YYYY-MM-DD")
	fs.StringVar(&opts.Profile, "profile", "", "Required: vector or hybrid; no implicit fallback")
	fs.StringVar(&opts.Unresolved, "unresolved", "report", "report, exclude or review")
	fs.StringVar(&opts.Snapshot, "snapshot", "", "Optional exact active snapshot ID; mismatch fails")
	fs.IntVar(&opts.Limit, "limit", 20, "Candidates per branch, 1..128; not a recall guarantee")
	fs.DurationVar(&opts.Timeout, "timeout", 30*time.Second, "Total deadline including startup, at most 5m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	opts.Corpus = env("REGULAGRAPH_QUERY_CORPUS_ID")
	opts.Build = env("REGULAGRAPH_BUILD_ID")
	opts.dsn = env("REGULAGRAPH_POSTGRES_DSN")
	opts.artifacts = env("REGULAGRAPH_ARTIFACTS_DIR")
	opts.native = env("REGULAGRAPH_QUERY_NATIVE_ENDPOINT")
	opts.qdrant = env("REGULAGRAPH_QDRANT_URL")
	opts.key = env("REGULAGRAPH_QDRANT_API_KEY")
	opts.rerankManifest = env("REGULAGRAPH_QUERY_RERANK_MANIFEST")
	opts.rerankHash = env("REGULAGRAPH_QUERY_RERANK_MANIFEST_SHA256")
	request, err := prepareEvidenceQuestion(opts)
	if err != nil || fs.NArg() != 0 {
		fmt.Fprintln(errOut, "Invalid query configuration: require question, AS_OF date, supported profile, bounded limits, corpus/build/storage settings and a loopback native endpoint")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	result, err := execute(bounded, opts, request)
	if err != nil {
		fmt.Fprintln(errOut, "Evidence query failed; check published snapshot, model/generation readiness and source integrity")
		return 1
	}
	if bounded.Err() != nil || result == nil || result.Evidence == nil || result.Answer != nil {
		fmt.Fprintln(errOut, "Evidence query returned an invalid or expired result")
		return 1
	}
	if err = domain.ValidateWire(result.Evidence, domain.DefaultWireLimits); err != nil {
		fmt.Fprintln(errOut, "Evidence output validation failed")
		return 1
	}
	if result.Evidence.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || result.Evidence.Meta.CorpusId != opts.Corpus || result.Evidence.Snapshot.GetCorpusId() != opts.Corpus || (opts.Snapshot != "" && result.Evidence.Snapshot.GetSnapshotId() != opts.Snapshot) {
		fmt.Fprintln(errOut, "Evidence output has a different corpus/snapshot or failed completion")
		return 1
	}
	raw, err := protojson.Marshal(result.Evidence)
	if err != nil {
		fmt.Fprintln(errOut, "Evidence serialization failed")
		return 1
	}
	reranking, err := queryRerankOutput(result, opts.rerankManifest != "")
	if err != nil {
		fmt.Fprintln(errOut, "Reranking output validation failed")
		return 1
	}
	// The JSON envelope is a CLI result, not a competing evidence wire contract.
	output := struct {
		Mode      string            `json:"mode"`
		Evidence  json.RawMessage   `json:"evidence"`
		Rejected  map[string]string `json:"rejected"`
		Reranking *queryRerankJSON  `json:"reranking,omitempty"`
	}{"evidence", raw, result.Rejected, reranking}
	if err = json.NewEncoder(out).Encode(output); err != nil {
		fmt.Fprintln(errOut, "Writing evidence output failed")
		return 1
	}
	return 0
}

func prepareEvidenceQuestion(o queryOptions) (*pb.QuestionRequest, error) {
	if (o.rerankManifest == "") != (o.rerankHash == "") {
		return nil, errors.New("reranker manifest and hash must be configured together")
	}
	if o.rerankHash != "" {
		decoded, err := hex.DecodeString(o.rerankHash)
		if err != nil || len(decoded) != sha256.Size || strings.ToLower(o.rerankHash) != o.rerankHash {
			return nil, errors.New("lowercase SHA-256 reranker pin required")
		}
	}
	if o.Corpus == "" || o.Build == "" || o.dsn == "" || o.artifacts == "" || o.Limit < 1 || o.Limit > 128 || o.Timeout <= 0 || o.Timeout > 5*time.Minute || len(o.Question) > 64<<10 {
		return nil, errors.New("missing configuration or invalid limits")
	}
	host, port, err := net.SplitHostPort(o.native)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("native plaintext endpoint must be a literal loopback address")
	}
	u, err := url.Parse(o.qdrant)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("qdrant origin required")
	}
	if u.Scheme == "http" && (net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback()) {
		return nil, errors.New("plaintext qdrant is restricted to literal loopback")
	}
	d, err := time.Parse("2006-01-02", o.AsOf)
	if err != nil {
		return nil, err
	}
	profiles := map[string]pb.RetrievalProfile{"vector": pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG, "hybrid": pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG}
	policies := map[string]pb.UnresolvedPolicy{"report": pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT, "exclude": pb.UnresolvedPolicy_UNRESOLVED_POLICY_EXCLUDE, "review": pb.UnresolvedPolicy_UNRESOLVED_POLICY_REQUIRE_REVIEW}
	if profiles[o.Profile] == 0 || policies[o.Unresolved] == 0 || strings.TrimSpace(o.Question) == "" {
		return nil, errors.New("explicit supported profile, policy and question required")
	}
	r := &pb.QuestionRequest{CorpusId: o.Corpus, Question: o.Question, ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE, RequestedProfile: profiles[o.Profile], TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: int32(d.Year()), Month: uint32(d.Month()), Day: uint32(d.Day())}, UnresolvedPolicy: policies[o.Unresolved]}}
	if o.Snapshot != "" {
		r.SnapshotId = proto.String(o.Snapshot)
	}
	return r, domain.ValidateWire(r, domain.DefaultWireLimits)
}

func executeEvidenceQuery(ctx context.Context, o queryOptions, request *pb.QuestionRequest) (*workflows.RAGResult, error) {
	var rerankModel *pb.ModelManifest
	if o.rerankManifest != "" {
		var err error
		rerankModel, err = config.LoadNativeModel(o.rerankManifest, o.rerankHash, pb.ModelTask_MODEL_TASK_RERANK)
		if err != nil {
			return nil, err
		}
	}
	repo, err := postgres.Open(ctx, postgres.Config{DSN: o.dsn, MaxConnections: 4, ConnectTimeout: 5 * time.Second, HealthTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	files, err := storage.NewFileStore(o.artifacts)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	conn, err := grpc.NewClient(o.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: o.Timeout}
	// Config pin excludes secrets and question, includes route/limits/policy/build.
	configBytes, _ := json.Marshal(map[string]any{"corpus": o.Corpus, "build": o.Build, "profile": o.Profile, "unresolved": o.Unresolved, "limit": o.Limit, "timeout_ns": int64(o.Timeout), "native": o.native, "qdrant": o.qdrant, "rrf_k": 60, "artifact_bytes": 64 << 20, "evidence_bytes": 4 << 20,
		"rerank_manifest_sha256": o.rerankHash, "rerank_pairs_per_batch": 32, "rerank_maximum_request_bytes": 4 << 20, "rerank_truncation_policy": "reject"})
	hash := sha256.Sum256(configBytes)
	fingerprint := &pb.ContentHash{Sha256: hex.EncodeToString(hash[:])}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(nonce[:])
	deadline, _ := ctx.Deadline()
	call := &pb.RequestContext{SchemaVersion: 1, RequestId: "query:" + id, TraceId: "trace:" + id, CorpusId: o.Corpus, AuthScopeRef: "operator:local-query", ConfigFingerprint: fingerprint, Deadline: timestamppb.New(deadline)}
	session := &workflows.RAGSession{Store: repo, OwnerID: "query:" + id, MaximumDuration: o.Timeout, SearchLimit: o.Limit, Factory: func(c context.Context, index *domain.PinnedIndex) (*workflows.RAGWorkflow, error) {
		models := []*pb.ModelManifest{index.Binding.Generation.DenseManifest}
		if rerankModel != nil {
			models = append(models, rerankModel)
		}
		native, err := inference.NewNativeClient(conn, models...)
		if err != nil {
			return nil, err
		}
		admitted := proto.Clone(call).(*pb.RequestContext)
		admitted.SnapshotRef = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
		if _, err = native.GetCapabilities(c, &pb.CapabilitiesRequest{Context: admitted}); err != nil {
			return nil, err
		}
		var reranker *retrieval.EvidenceReranker
		if rerankModel != nil {
			reranker, err = retrieval.NewEvidenceReranker(native, retrieval.EvidenceRerankConfig{Model: rerankModel, MaximumCandidates: 2 * o.Limit, PairsPerBatch: 32, MaximumRequestBytes: 4 << 20})
			if err != nil {
				return nil, err
			}
		}
		producer := &pb.ProducerManifest{Software: "regulagraph-query", Build: o.Build, SchemaVersion: 1, ConfigHash: fingerprint, Models: []*pb.ModelManifest{index.Binding.Generation.DenseManifest}}
		prepared, err := workflows.PreparePublishedQuery(c, index, repo, files, native, nil, workflows.PublishedQueryConfig{QdrantCredentials: map[string]string{o.qdrant: o.key}, HTTPClient: httpClient, Profile: request.RequestedProfile,
			Fusion:    retrieval.RRFConfig{K: 60, MaximumPerBranch: o.Limit, MaximumTotalInputs: 2 * o.Limit, Weights: map[pb.RetrieverKind]float64{pb.RetrieverKind_RETRIEVER_KIND_DENSE: 1, pb.RetrieverKind_RETRIEVER_KIND_BM25: 1}},
			Hydration: retrieval.HydrationConfig{MaximumCandidates: 2 * o.Limit, MaximumArtifactBytes: 64 << 20, MaximumEvidenceBytes: 4 << 20, Producer: producer}, MaximumLexicalBytes: 64 << 20, Reranker: reranker})
		if err != nil {
			return nil, err
		}
		return prepared.Bind(c, index)
	}}
	return session.SearchQuestion(ctx, request, call)
}
