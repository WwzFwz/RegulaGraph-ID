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
// Status: evidence runtime aktif untuk vector/hybrid dengan shared clients dan satu
// cache generation immutable; lease/evidence tetap baru per request. Answering dan
// graph profiles belum diaktifkan. Close dipanggil setelah request selesai/drain.
// Integrasi berikutnya: generator/tokenizer terpin dan graph readiness.
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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/workflows"
)

// EvidenceRuntimeConfig is operator configuration, never a request payload.
// This local runtime serves one explicitly authorized corpus and profile.
type EvidenceRuntimeConfig struct {
	DSN, ArtifactRoot, NativeEndpoint, QdrantEndpoint, QdrantKey string
	Corpus, AuthScope, Build                                     string
	Profile                                                      pb.RetrievalProfile
	Limit                                                        int
	Timeout                                                      time.Duration
}

type EvidenceRuntime struct {
	config      EvidenceRuntimeConfig
	repo        *postgres.Repository
	files       *storage.FileStore
	connection  *grpc.ClientConn
	transport   *http.Transport
	http        *http.Client
	fingerprint *pb.ContentHash
	preparing   chan struct{}
	cached      *workflows.PreparedQuery
	binding     domain.IndexCatalogBinding
	native      *inference.NativeClient
}

// Set only by this process's HTTP middleware, never from a caller header.
type evidenceRequestIdentity struct{}

func OpenEvidenceRuntime(ctx context.Context, cfg EvidenceRuntimeConfig) (*EvidenceRuntime, error) {
	host, _, err := net.SplitHostPort(cfg.NativeEndpoint)
	if ctx == nil || err != nil || !net.ParseIP(host).IsLoopback() || cfg.DSN == "" || cfg.ArtifactRoot == "" || cfg.QdrantEndpoint == "" || cfg.Build == "" || cfg.Limit < 1 || cfg.Limit > 128 || cfg.Timeout < time.Second || cfg.Timeout > 5*time.Minute || (cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG && cfg.Profile != pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG) {
		return nil, errors.New("invalid local evidence runtime configuration")
	}
	for _, id := range []string{cfg.Corpus, cfg.AuthScope, cfg.Build} {
		if err = domain.ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: cfg.Corpus, RecordId: id}, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	// Exclude credentials from the observable fingerprint. Include the behavior
	// and route pins; the actual model generation is added from the catalog.
	raw, _ := json.Marshal(struct {
		Corpus, Scope, Build, Native, Qdrant string
		Profile                              pb.RetrievalProfile
		Limit                                int
		Timeout                              time.Duration
	}{cfg.Corpus, cfg.AuthScope, cfg.Build, cfg.NativeEndpoint, cfg.QdrantEndpoint, cfg.Profile, cfg.Limit, cfg.Timeout})
	hash := sha256.Sum256(raw)
	r := &EvidenceRuntime{config: cfg, fingerprint: &pb.ContentHash{Sha256: hex.EncodeToString(hash[:])}, preparing: make(chan struct{}, 1)}
	r.repo, err = postgres.Open(ctx, postgres.Config{DSN: cfg.DSN, MaxConnections: 8, ConnectTimeout: 5 * time.Second, HealthTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	r.files, err = storage.NewFileStore(cfg.ArtifactRoot)
	if err != nil {
		r.Close()
		return nil, err
	}
	r.connection, err = grpc.NewClient(cfg.NativeEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		r.Close()
		return nil, err
	}
	r.transport = http.DefaultTransport.(*http.Transport).Clone()
	r.http = &http.Client{Transport: r.transport, Timeout: cfg.Timeout}
	return r, nil
}

func (r *EvidenceRuntime) Close() {
	if r == nil {
		return
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

func (r *EvidenceRuntime) prepare(ctx context.Context, index *domain.PinnedIndex, call *pb.RequestContext) (*workflows.PreparedQuery, *inference.NativeClient, error) {
	select {
	case r.preparing <- struct{}{}:
		defer func() { <-r.preparing }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	if r.cached != nil && sameEvidenceBinding(r.binding, index.Binding) {
		return r.cached, r.native, nil
	}
	native, err := inference.NewNativeClient(r.connection, index.Binding.Generation.DenseManifest)
	if err != nil {
		return nil, nil, err
	}
	admitted := proto.Clone(call).(*pb.RequestContext)
	admitted.SnapshotRef = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
	if _, err = native.GetCapabilities(ctx, &pb.CapabilitiesRequest{Context: admitted}); err != nil {
		return nil, nil, err
	}
	producer := &pb.ProducerManifest{Software: "regulagraph-evidence-api", Build: r.config.Build, SchemaVersion: 1, ConfigHash: proto.Clone(r.fingerprint).(*pb.ContentHash), Models: []*pb.ModelManifest{proto.Clone(index.Binding.Generation.DenseManifest).(*pb.ModelManifest)}}
	n := r.config.Limit
	prepared, err := workflows.PreparePublishedQuery(ctx, index, r.repo, r.files, native, nil, workflows.PublishedQueryConfig{QdrantCredentials: map[string]string{r.config.QdrantEndpoint: r.config.QdrantKey}, HTTPClient: r.http, Profile: r.config.Profile, MaximumLexicalBytes: 64 << 20, Fusion: retrieval.RRFConfig{K: 60, MaximumPerBranch: n, MaximumTotalInputs: 2 * n, Weights: map[pb.RetrieverKind]float64{pb.RetrieverKind_RETRIEVER_KIND_DENSE: 1, pb.RetrieverKind_RETRIEVER_KIND_BM25: 1}}, Hydration: retrieval.HydrationConfig{MaximumCandidates: 2 * n, MaximumArtifactBytes: 64 << 20, MaximumEvidenceBytes: 4 << 20, Producer: producer}})
	if err != nil {
		return nil, nil, err
	}
	r.binding = index.Binding
	r.binding.Generation = proto.Clone(index.Binding.Generation).(*pb.IndexGeneration)
	r.cached, r.native = prepared, native
	return prepared, native, nil
}

func (r *EvidenceRuntime) Search(ctx context.Context, question *pb.QuestionRequest) (*pb.EvidenceBundle, error) {
	if question == nil || question.CorpusId != r.config.Corpus || question.RequestedProfile != r.config.Profile {
		return nil, errors.New("unauthorized corpus or unsupported profile")
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	call, err := r.call(ctx)
	if err != nil {
		return nil, err
	}
	session := &workflows.RAGSession{Store: r.repo, OwnerID: call.RequestId, MaximumDuration: r.config.Timeout, SearchLimit: r.config.Limit, Factory: func(c context.Context, index *domain.PinnedIndex) (*workflows.RAGWorkflow, error) {
		prepared, _, e := r.prepare(c, index, call)
		if e != nil {
			return nil, e
		}
		return prepared.Bind(c, index)
	}}
	result, err := session.SearchQuestion(ctx, question, call)
	if err != nil {
		return nil, err
	}
	return result.Evidence, nil
}

// Ready checks current snapshot/catalog, native capabilities and collection
// shape without claiming corpus quality or answer-generator readiness.
func (r *EvidenceRuntime) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
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
	_, native, err := r.prepare(ctx, index, call)
	if err != nil {
		return err
	}
	call.SnapshotRef = proto.Clone(index.Snapshot).(*pb.SnapshotRef)
	if _, err = native.GetCapabilities(ctx, &pb.CapabilitiesRequest{Context: call}); err != nil {
		return err
	}
	store, err := qdrant.New(index.Binding.Endpoint, r.config.QdrantKey, r.http, qdrant.Binding{Collection: index.Binding.Collection, CorpusID: r.config.Corpus, Generation: index.Binding.Generation})
	if err != nil {
		return err
	}
	return store.OpenExistingCollection(ctx)
}
