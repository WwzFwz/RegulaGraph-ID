// Opt-in cross-runtime integration: authenticated Rust CHUNK fixture -> population
// CLI -> Go registry/bootstrap -> durable INDEX RPC -> C++ embedding -> Qdrant ->
// pinned hybrid evidence. Source job rows are synthetic seeds, but statistics and
// vectors must be computed by the actual supplied executables/model. This tests
// interoperability and provenance, not legal gold quality or release performance.
// Use disposable PostgreSQL/Qdrant and a dedicated worker artifact root. Missing
// prerequisites skip explicitly; a skipped test is never native acceptance PASS.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/adapters/worker"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/workflows"
)

func TestNativeIndexPipeline(t *testing.T) {
	if os.Getenv("REGULAGRAPH_TEST_NATIVE_INDEX") != "1" {
		t.Skip("explicit native INDEX integration opt-in required")
	}
	required := func(name string) string {
		t.Helper()
		value := os.Getenv(name)
		if value == "" {
			t.Fatal("missing", name)
		}
		return value
	}
	fixture := required("REGULAGRAPH_INDEX_FIXTURE_DIR")
	root := required("REGULAGRAPH_TEST_INDEX_ARTIFACT_ROOT")
	lexical := required("REGULAGRAPH_TEST_LEXICAL_EXE")
	endpoint := required("REGULAGRAPH_TEST_QDRANT_ENDPOINT")
	nativeEndpoint := required("REGULAGRAPH_TEST_NATIVE_ENDPOINT")
	workerEndpoint := required("REGULAGRAPH_TEST_WORKER_ENDPOINT")
	for _, address := range []string{nativeEndpoint, workerEndpoint} {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			t.Fatal("test RPC endpoint must be loopback")
		}
	}
	model, err := config.LoadNativeModel(required("REGULAGRAPH_TEST_NATIVE_EMBED_MANIFEST"), required("REGULAGRAPH_TEST_NATIVE_EMBED_SHA256"), pb.ModelTask_MODEL_TASK_EMBED)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dsn := isolatedIndexTestDSN(t, ctx, required("REGULAGRAPH_TEST_POSTGRES_DSN"))
	repo, err := postgres.Open(ctx, postgres.Config{DSN: dsn, MaxConnections: 6, HealthTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	migrations, err := filepath.Abs("../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyMigrations(ctx, os.DirFS(migrations)); err != nil {
		t.Fatal(err)
	}
	files, err := storage.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	seedFiles, err := storage.NewFileStore(filepath.Join(fixture, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	defer seedFiles.Close()
	source := new(pb.DocumentBatch)
	raw, err := os.ReadFile(filepath.Join(fixture, "source.pb"))
	if err != nil {
		t.Fatal(err)
	}
	if err = domain.DecodeWire(raw, source, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	source.Context.SnapshotRef = nil
	corpus, scope := source.Meta.CorpusId, source.Context.AuthScopeRef
	// Retain original raw/normalized text and mapping bytes; unlike vector-only
	// fixtures, Rust must verify and render these actual artifacts again.
	for _, text := range source.TextArtifacts {
		for _, ref := range []*pb.ArtifactRef{text.RawTextRef, text.NormalizedTextRef, text.MappingRef} {
			data, e := seedFiles.ReadVerified(ctx, ref, 16<<20)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = files.Put(ctx, ref, bytes.NewReader(data)); e != nil {
				t.Fatal(e)
			}
			if e = repo.RegisterArtifact(ctx, corpus, ref); e != nil {
				t.Fatal(e)
			}
		}
	}
	for i, blob := range source.Sources {
		source.Observations = append(source.Observations, &pb.SourceObservation{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: fmt.Sprintf("observation:native:%d", i)}, PortalId: "fixture", DetailUrl: "https://example.org/native-fixture.pdf", ResolvedUrl: "https://example.org/native-fixture.pdf", FetchedAt: timestamppb.Now(), Status: pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE, SourceBlobId: proto.String(blob.Meta.RecordId)})
	}
	raw, err = proto.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	sourceRef := &pb.ArtifactRef{SchemaVersion: 1, ArtifactId: "artifact:document-batch:" + digest, ContentHash: &pb.ContentHash{Sha256: digest}, ByteSize: uint64(len(raw)), MediaType: domain.DocumentBatchMediaType, StorageKey: "sha256/" + digest[:2] + "/" + digest[2:4] + "/" + digest + ".bin"}
	if _, err = files.Put(ctx, sourceRef, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if err = repo.RegisterArtifact(ctx, corpus, sourceRef); err != nil {
		t.Fatal(err)
	}
	job := "job:native-source"
	producer := source.DependencyManifest.ProducerManifest
	request := &pb.IngestionRequest{CorpusId: corpus, Operation: pb.JobOperation_JOB_OPERATION_INGEST, IdempotencyKey: job, Sources: []*pb.SourceLocator{{PortalId: "fixture", Locator: &pb.SourceLocator_Url{Url: "https://example.org/native-fixture.pdf"}}}, ConfigManifest: producer}
	requestRaw, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	cp := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "checkpoint:native-source"}, JobId: job, Stage: pb.JobStage_JOB_STAGE_CHUNK, Fence: 1, Manifest: producer, CompletedBatchKeys: []string{sourceRef.ArtifactId}, ArtifactHashes: []*pb.ContentHash{sourceRef.ContentHash}, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	if err = domain.ValidateWire(cp, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	cpRaw, err := proto.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload) VALUES($1,$2,1,3,$3,$4,$1,$4,$5)`, job, corpus, int16(cp.Stage), fmt.Sprintf("%x", sha256.Sum256(requestRaw)), requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,1,$4,$5,$6)`, cp.Meta.RecordId, job, int16(cp.Stage), cpRaw, fmt.Sprintf("%x", sha256.Sum256(cpRaw)), int16(cp.TerminalStatus))
	if err != nil {
		t.Fatal(err)
	}
	pub := "publication:native-pipeline"
	prepared, err := PrepareInitialSourceSnapshot(ctx, repo, files, files, InitialSourceSnapshotConfig{CorpusID: corpus, PublicationID: pub, GenerationID: "generation:native-pipeline", AuthScope: scope}, []InitialIndexSource{{SourceJobID: job, DocumentBatch: sourceRef}})
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	write := func(name string, message proto.Message) string {
		t.Helper()
		raw, e := proto.Marshal(message)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(temp, name)
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	snapshotPath := write("snapshot.pb", prepared.Snapshot)
	sourcePath := write("source-ref.pb", prepared.Sources[0].DocumentBatch)
	base := []string{"--artifacts", root, "--snapshot", snapshotPath, "--auth-scope", scope, "--source-ref", sourcePath}
	run := func(command string, args ...string) {
		t.Helper()
		commandArgs := append([]string{command}, base...)
		commandArgs = append(commandArgs, args...)
		output, e := exec.CommandContext(ctx, lexical, commandArgs...).CombinedOutput()
		if e != nil {
			t.Fatalf("Rust population %s: %v %s", command, e, output)
		}
	}
	vocabularyPath := filepath.Join(temp, "vocabulary.txt")
	run("vocabulary", "--output", vocabularyPath)
	vocabulary, err := os.ReadFile(vocabularyPath)
	if err != nil {
		t.Fatal(err)
	}
	dictionary, err := PrepareLexicalDictionary(ctx, repo, files, corpus, strings.Split(strings.TrimSuffix(string(vocabulary), "\n"), "\n"))
	if err != nil {
		t.Fatal(err)
	}
	dictionaryPath := write("dictionary-ref.pb", dictionary)
	statisticsPath := filepath.Join(temp, "statistics-ref.pb")
	run("freeze", "--dictionary-ref", dictionaryPath, "--statistics-id", "statistics:native-pipeline", "--k1", "1.2", "--b", "0.75", "--output", statisticsPath)
	statistics := new(pb.ArtifactRef)
	raw, err = os.ReadFile(statisticsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = domain.DecodeWire(raw, statistics, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	collection := fmt.Sprintf("native_index_%d", time.Now().UnixNano())
	inventory, err := BootstrapInitialIndex(ctx, repo, files, files, InitialIndexBootstrapConfig{PublicationID: pub, Endpoint: endpoint, Collection: collection, AuthScope: scope, OntologyVersion: "ontology:fixture", Snapshot: prepared.Snapshot, Model: model, Dictionary: dictionary, Statistics: statistics, ChunksPerBatch: 2}, prepared.Sources)
	if err != nil {
		t.Fatal(err)
	}
	client, err := worker.New(workerEndpoint, insecure.NewCredentials(), worker.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	processor, err := NewInitialIndexJobProcessor(repo, files, client, scope)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := workflows.NewIndexExecutor(repo, processor, workflows.IndexExecutorConfig{OwnerID: "native-index-test", Lease: time.Minute, CallTimeout: 50 * time.Second, CancellationPoll: 50 * time.Millisecond, RetryBase: time.Second, RetryMax: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for range inventory.Assignments {
		_, response, e := executor.RunOnce(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if response.Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
			t.Fatal("native INDEX failed", response)
		}
	}
	physical, err := qdrant.New(endpoint, "", &http.Client{Timeout: 10 * time.Second}, qdrant.Binding{Collection: collection, CorpusID: corpus, Generation: inventory.Binding.Generation})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, e := http.NewRequestWithContext(cleanup, http.MethodDelete, endpoint+"/collections/"+collection, nil)
		if e == nil {
			response, e := http.DefaultClient.Do(req)
			if e == nil {
				response.Body.Close()
			}
		}
	}()
	published, err := PublishCompletedVectorIndex(ctx, repo, files, physical, pub)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(published, prepared.Snapshot) {
		t.Fatal("published snapshot drift")
	}
	nativeConn, err := grpc.NewClient(nativeEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer nativeConn.Close()
	native, err := inference.NewNativeClient(nativeConn, model)
	if err != nil {
		t.Fatal(err)
	}
	session := &workflows.RAGSession{Store: repo, OwnerID: "native-query-test", MaximumDuration: 30 * time.Second, SearchLimit: 8, Factory: func(c context.Context, index *domain.PinnedIndex) (*workflows.RAGWorkflow, error) {
		query, e := workflows.PreparePublishedQuery(c, index, repo, files, native, nil, workflows.PublishedQueryConfig{QdrantCredentials: map[string]string{endpoint: ""}, HTTPClient: &http.Client{Timeout: 10 * time.Second}, Profile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, MaximumLexicalBytes: 16 << 20, Fusion: retrieval.RRFConfig{K: 60, MaximumPerBranch: 8, MaximumTotalInputs: 16, Weights: map[pb.RetrieverKind]float64{pb.RetrieverKind_RETRIEVER_KIND_DENSE: 1, pb.RetrieverKind_RETRIEVER_KIND_BM25: 1}}, Hydration: retrieval.HydrationConfig{MaximumCandidates: 16, MaximumArtifactBytes: 16 << 20, MaximumEvidenceBytes: 1 << 20, Producer: inventory.Assignments[0].Plan.Producer}})
		if e != nil {
			return nil, e
		}
		return query.Bind(c, index)
	}}
	question := &pb.QuestionRequest{Question: "Apa ketentuan izin usaha?", CorpusId: corpus, ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE, RequestedProfile: pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_RAG, TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}
	call := &pb.RequestContext{SchemaVersion: 1, RequestId: "request:native-query", TraceId: "trace:native-query", CorpusId: corpus, AuthScopeRef: scope, ConfigFingerprint: inventory.Assignments[0].Plan.Producer.ConfigHash, Deadline: timestamppb.New(time.Now().Add(30 * time.Second))}
	result, err := session.SearchQuestion(ctx, question, call)
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != nil || len(result.Search.Branches) != 2 || len(result.Evidence.Items) == 0 || !proto.Equal(result.Evidence.Snapshot, published) {
		t.Fatal("native hybrid query missing pinned evidence")
	}
	for _, evidence := range result.Evidence.Items {
		if len(evidence.SourceRefs) == 0 {
			t.Fatal("missing evidence provenance")
		}
	}
	t.Logf("native pipeline: %d source chunks, %d INDEX jobs, %d hydrated evidence items; model %s@%s", len(source.Chunks), len(inventory.Assignments), len(result.Evidence.Items), model.ModelId, model.Version)
}
