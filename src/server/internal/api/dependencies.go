// Menyusun instance adapter, konfigurasi, dan workflow bagi request aplikasi.
//
// Peran dalam komponen:
// Menjadi composition root sehingga route dapat diuji dengan dependency pengganti.
//
// Kontrak integrasi dan perhatian implementasi:
// Hindari membuat model/koneksi baru per request; perhatikan lifecycle, thread safety, dan batas concurrency.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
//
// [STORAGE] Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.
//
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: empat profil evidence dengan shared clients dan snapshot-bound graph
// resources. Retired resources survive active borrowers; leases remain per request.
// Optional local answering shares one admitted generator; Close runs after drain.
// Integrasi berikutnya: streaming dan acceptance corpus nyata.
// Bukti verifikasi: Test partial startup cleanup and dependency failure without opening connections during package initialization.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/neo4j"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

// EvidenceRuntimeConfig is operator configuration, never a request payload.
// This local runtime serves one explicitly authorized corpus and profile.
type EvidenceRuntimeConfig struct {
	TimeZone                                                     string
	Normalization                                                query.NormalizationMode
	DSN, ArtifactRoot, NativeEndpoint, QdrantEndpoint, QdrantKey string
	Corpus, AuthScope, Build                                     string
	Profile                                                      pb.RetrievalProfile
	Limit                                                        int
	Timeout                                                      time.Duration
	GraphPath, GraphHash, GraphUsername, GraphPassword           string
	EnableAnswers                                                bool
	AnswerPath, AnswerHash, AnswerKey                            string
}

type EvidenceRuntime struct {
	timeZone    *time.Location
	config      EvidenceRuntimeConfig
	repo        *postgres.Repository
	files       *storage.FileStore
	connection  *grpc.ClientConn
	transport   *http.Transport
	http        *http.Client
	fingerprint *pb.ContentHash
	preparing   chan struct{}
	resources   queryResourceCache
	graphConfig *config.QueryGraphConfig
	answer      *workflows.LocalAnswerRuntime
}

// Set only by this process's HTTP middleware, never from a caller header.
type evidenceRequestIdentity struct{}

func OpenEvidenceRuntime(ctx context.Context, cfg EvidenceRuntimeConfig) (*EvidenceRuntime, error) {
	mode, modeErr := query.ParseNormalizationMode(string(cfg.Normalization))
	if modeErr != nil {
		return nil, modeErr
	}
	cfg.Normalization = mode
	zone, zoneErr := query.LoadQueryTimeZone(cfg.TimeZone)
	if zoneErr != nil {
		return nil, zoneErr
	}
	graphProfile := cfg.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG || cfg.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG
	host, port, err := net.SplitHostPort(cfg.NativeEndpoint)
	number, portErr := strconv.Atoi(port)
	nativeRequired := cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG
	if ctx == nil || ((nativeRequired || cfg.NativeEndpoint != "") && (err != nil || portErr != nil || number < 1 || number > 65535 || !net.ParseIP(host).IsLoopback())) || cfg.DSN == "" || cfg.ArtifactRoot == "" || cfg.QdrantEndpoint == "" || cfg.Build == "" || cfg.Limit < 1 || cfg.Limit > 128 || cfg.Timeout < time.Second || cfg.Timeout > 5*time.Minute || (!graphProfile && cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG && cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG) || cfg.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG && cfg.Limit > 85 {
		return nil, errors.New("invalid local evidence runtime configuration")
	}
	var graphConfig *config.QueryGraphConfig
	if graphProfile {
		if cfg.GraphUsername == "" || cfg.GraphPassword == "" {
			return nil, errors.New("graph credentials required")
		}
		graphConfig, err = config.LoadQueryGraph(cfg.GraphPath, cfg.GraphHash, cfg.Corpus)
		if err != nil {
			return nil, err
		}
	} else if cfg.GraphPath != "" || cfg.GraphHash != "" {
		return nil, errors.New("graph configuration requires explicit graph profile")
	}
	for _, id := range []string{cfg.Corpus, cfg.AuthScope, cfg.Build} {
		if err = domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.Corpus, RecordId: id}, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	var answerConfig *config.AnswerGeneratorConfig
	if cfg.EnableAnswers {
		answerConfig, err = config.LoadAnswerGenerator(cfg.AnswerPath, cfg.AnswerHash, cfg.Corpus)
		if err != nil {
			return nil, err
		}
		branches := 1
		if cfg.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG {
			branches = 2
		}
		if cfg.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG {
			branches = 3
		}
		if answerConfig.MaximumEvidence < branches*cfg.Limit {
			return nil, errors.New("answer evidence budget must cover retrieval candidates")
		}
	} else if cfg.AnswerPath != "" || cfg.AnswerHash != "" || cfg.AnswerKey != "" {
		return nil, errors.New("answer configuration requires explicit answer capability")
	}
	// Exclude credentials from the observable fingerprint. Include the behavior
	// and route pins; the actual model generation is added from the catalog.
	raw, _ := json.Marshal(struct {
		Corpus, Scope, Build, Native, Qdrant string
		TimeZone, TemporalPolicy             string
		Normalization                        query.NormalizationMode
		Profile                              pb.RetrievalProfile
		Limit                                int
		Timeout                              time.Duration
		GraphHash                            string
		AnswerHash                           string
	}{cfg.Corpus, cfg.AuthScope, cfg.Build, cfg.NativeEndpoint, cfg.QdrantEndpoint, cfg.TimeZone, query.TemporalPolicyVersion, cfg.Normalization, cfg.Profile, cfg.Limit, cfg.Timeout, cfg.GraphHash, cfg.AnswerHash})
	hash := sha256.Sum256(raw)
	r := &EvidenceRuntime{config: cfg, timeZone: zone, fingerprint: &pb.ContentHash{Sha256: hex.EncodeToString(hash[:])}, preparing: make(chan struct{}, 1), graphConfig: graphConfig}
	if answerConfig != nil {
		r.answer, err = workflows.OpenLocalAnswer(ctx, answerConfig, cfg.AnswerKey, cfg.Build, cfg.Timeout)
		if err != nil {
			return nil, err
		}
	}
	r.repo, err = postgres.Open(ctx, postgres.Config{DSN: cfg.DSN, MaxConnections: 8, ConnectTimeout: 5 * time.Second, HealthTimeout: 5 * time.Second})
	if err != nil {
		r.Close()
		return nil, err
	}
	r.files, err = storage.NewFileStore(cfg.ArtifactRoot)
	if err != nil {
		r.Close()
		return nil, err
	}
	if nativeRequired {
		r.connection, err = grpc.NewClient(cfg.NativeEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			r.Close()
			return nil, err
		}
	}
	r.transport = http.DefaultTransport.(*http.Transport).Clone()
	r.http = &http.Client{Transport: r.transport, Timeout: cfg.Timeout}
	return r, nil
}

func (r *EvidenceRuntime) Close() {
	if r == nil {
		return
	}
	r.resources.close()
	if r.answer != nil {
		r.answer.Close()
	}
	if r.connection != nil {
		r.connection.Close()
	}
	if r.files != nil {
		r.files.Close()
	}
	if r.repo != nil {
		r.repo.Close()
	}
	if r.transport != nil {
		r.transport.CloseIdleConnections()
	}
}

func (r *EvidenceRuntime) call(ctx context.Context) (*pb.RequestContext, error) {
	id, _ := ctx.Value(evidenceRequestIdentity{}).(string)
	if id == "" {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		id = "api:" + hex.EncodeToString(nonce[:])
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("bounded request required")
	}
	return &pb.RequestContext{SchemaVersion: 1, RequestId: id, TraceId: "trace:" + id, CorpusId: r.config.Corpus, AuthScopeRef: r.config.AuthScope, ConfigFingerprint: proto.Clone(r.fingerprint).(*pb.ContentHash), Deadline: timestamppb.New(deadline)}, nil
}

func sameEvidenceBinding(a, b domain.IndexCatalogBinding) bool {
	return a.PublicationID == b.PublicationID && a.Fence == b.Fence && a.Endpoint == b.Endpoint && a.Collection == b.Collection && proto.Equal(a.Generation, b.Generation)
}

func (r *EvidenceRuntime) prepare(ctx context.Context, index *domain.PinnedIndex, call *pb.RequestContext) (*queryResource, func(), error) {
	select {
	case r.preparing <- struct{}{}:
		defer func() { <-r.preparing }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	if resource, release := r.resources.acquire(index); resource != nil {
		return resource, release, nil
	}
	admitted := proto.Clone(call).(*pb.RequestContext)
	admitted.SnapshotRef = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
	resource := &queryResource{binding: index.Binding}
	resource.binding.Generation = proto.Clone(index.Binding.Generation).(*pb.IndexGeneration)
	success := false
	defer func() {
		if !success && resource.close != nil {
			resource.close()
		}
	}()
	models := []*pb.ModelManifest{}
	var err error
	if r.config.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_GRAPH_RAG {
		resource.native, err = inference.NewNativeClient(r.connection, index.Binding.Generation.DenseManifest)
		if err != nil {
			return nil, nil, err
		}
		if _, err = resource.native.GetCapabilities(ctx, &pb.CapabilitiesRequest{Context: admitted}); err != nil {
			return nil, nil, err
		}
		models = append(models, proto.Clone(index.Binding.Generation.DenseManifest).(*pb.ModelManifest))
	}
	producer := &pb.ProducerManifest{Software: "regulagraph-evidence-api", Build: r.config.Build, SchemaVersion: 1, ConfigHash: proto.Clone(r.fingerprint).(*pb.ContentHash), Models: models}
	var graphConfig *workflows.GraphQueryConfig
	if r.graphConfig != nil {
		view, e := r.repo.LoadPinnedGraph(ctx, index.Pin, r.config.AuthScope)
		if e != nil {
			return nil, nil, e
		}
		if view.Catalog.Endpoint != r.graphConfig.Endpoint || view.Catalog.Database != r.graphConfig.Database {
			return nil, nil, errors.New("published graph route is not authorized")
		}
		resource.graph, e = neo4j.New(neo4j.Config{URI: r.graphConfig.Endpoint, Database: r.graphConfig.Database, Username: r.config.GraphUsername, Password: r.config.GraphPassword, PoolSize: 8, Timeout: r.config.Timeout}, view.Catalog.Binding)
		if e != nil {
			return nil, nil, e
		}
		resource.close = func() {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = resource.graph.Close(c)
		}
		if _, e = resource.graph.OpenReader(ctx, view); e != nil {
			return nil, nil, e
		}
		seeds, e := workflows.NewQueryGraphSeedResolver(r.repo, r.graphConfig.Linking)
		if e != nil {
			return nil, nil, e
		}
		graphConfig = &workflows.GraphQueryConfig{Backend: resource.graph, Seeds: seeds, Traversal: r.graphConfig.Traversal}
		resource.snapshot = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
		hash, _ := r.graphConfig.Linking.Fingerprint()
		producer.InputHashes = []*pb.ContentHash{{Sha256: r.config.GraphHash}, {Sha256: hash}}
	}
	n := r.config.Limit
	branches := 1
	if r.config.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG {
		branches = 2
	}
	if r.config.Profile == pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG {
		branches = 3
	}
	var answerWorkflow *workflows.EvidenceAnswerWorkflow
	if r.answer != nil {
		answerWorkflow = r.answer.Workflow
	}
	resource.prepared, err = workflows.PreparePublishedQuery(ctx, index, r.repo, r.files, resource.native, answerWorkflow, workflows.PublishedQueryConfig{Normalization: r.config.Normalization, QdrantCredentials: map[string]string{r.config.QdrantEndpoint: r.config.QdrantKey}, HTTPClient: r.http, Profile: r.config.Profile, MaximumLexicalBytes: 64 << 20, Graph: graphConfig, Fusion: retrieval.RRFConfig{K: 60, MaximumPerBranch: n, MaximumTotalInputs: branches * n, Weights: map[pb.RetrieverKind]float64{pb.RetrieverKind_RETRIEVER_KIND_DENSE: 1, pb.RetrieverKind_RETRIEVER_KIND_BM25: 1, pb.RetrieverKind_RETRIEVER_KIND_GRAPH: 1}}, Hydration: retrieval.HydrationConfig{MaximumCandidates: branches * n, MaximumArtifactBytes: 64 << 20, MaximumEvidenceBytes: 4 << 20, Producer: producer}})
	if err != nil {
		return nil, nil, err
	}
	// install owns cleanup even if Close raced preparation.
	success = true
	release, ok := r.resources.install(resource)
	if !ok {
		return nil, nil, errors.New("query runtime closed")
	}
	return resource, release, nil
}

func (r *EvidenceRuntime) Search(ctx context.Context, question *pb.QuestionRequest) (*workflows.RAGResult, error) {
	return r.query(ctx, question, false)
}
func (r *EvidenceRuntime) AnswerEnabled() bool { return r != nil && r.answer != nil }

func (r *EvidenceRuntime) Answer(ctx context.Context, question *pb.QuestionRequest) (*workflows.RAGResult, error) {
	if r.answer == nil {
		return nil, errors.New("answer capability is disabled")
	}
	return r.query(ctx, question, true)
}
func (r *EvidenceRuntime) query(ctx context.Context, question *pb.QuestionRequest, answer bool) (*workflows.RAGResult, error) {
	if question == nil || question.CorpusId != r.config.Corpus || question.RequestedProfile != r.config.Profile {
		return nil, errors.New("unauthorized corpus or unsupported profile")
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	call, err := r.call(ctx)
	if err != nil {
		return nil, err
	}
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	session := &workflows.RAGSession{TimeZone: r.timeZone, Store: r.repo, OwnerID: call.RequestId, MaximumDuration: r.config.Timeout, SearchLimit: r.config.Limit, Factory: func(c context.Context, index *domain.PinnedIndex) (*workflows.RAGWorkflow, error) {
		resource, done, e := r.prepare(c, index, call)
		if e != nil {
			return nil, e
		}
		release = done
		return resource.prepared.Bind(c, index)
	}}
	if answer {
		return session.AnswerQuestion(ctx, question, call)
	}
	return session.SearchQuestion(ctx, question, call)
}

// Ready checks current snapshot/catalog, native capabilities and collection
// shape plus admitted generator when enabled; this does not prove corpus quality.
func (r *EvidenceRuntime) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if r.answer != nil {
		if err := r.answer.Ready(ctx); err != nil {
			return err
		}
	}
	call, err := r.call(ctx)
	if err != nil {
		return err
	}
	pin, err := r.repo.PinActiveSnapshot(ctx, r.config.Corpus, "pin:"+call.RequestId, call.RequestId, 15*time.Second)
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.repo.ReleaseSnapshotPin(c, pin.LeaseID, call.RequestId)
	}()
	index, err := r.repo.LoadPinnedIndex(ctx, pin)
	if err != nil {
		return err
	}
	resource, release, err := r.prepare(ctx, index, call)
	if err != nil {
		return err
	}
	defer release()
	call.SnapshotRef = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
	if resource.native != nil {
		if _, err = resource.native.GetCapabilities(ctx, &pb.CapabilitiesRequest{Context: call}); err != nil {
			return err
		}
	}
	if resource.graph != nil {
		view, e := r.repo.LoadPinnedGraph(ctx, pin, r.config.AuthScope)
		if e != nil {
			return e
		}
		if _, e = resource.graph.OpenReader(ctx, view); e != nil {
			return e
		}
	}
	store, err := qdrant.New(index.Binding.Endpoint, r.config.QdrantKey, r.http, qdrant.Binding{Collection: index.Binding.Collection, CorpusID: r.config.Corpus, Generation: index.Binding.Generation})
	if err != nil {
		return err
	}
	return store.OpenExistingCollection(ctx)
}
