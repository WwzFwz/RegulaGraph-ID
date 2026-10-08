// Tests the declared initial snapshot through authenticated Rust-derived source
// fixtures, PostgreSQL authority/intent and real Qdrant. Source job/checkpoint
// rows and normalized vectors are synthetic seeds, not a run of live ingestion
// or inference. Both disposable endpoint env vars are required; skip is not PASS.
// Fault injection proves a lost write reply leaves no publishable partial state.
// Both legacy logical refs and Rust-style content-addressed batch refs must reach
// publication and pinned hydration without rewriting their logical record IDs.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval"
)

type indexMemoryArtifacts map[string][]byte

func (a indexMemoryArtifacts) ReadVerified(_ context.Context, r *pb.ArtifactRef, _ uint64) ([]byte, error) {
	return append([]byte(nil), a[r.ArtifactId]...), nil
}

type lostIndexReply struct {
	*qdrant.Store
	fail bool
}

func (b *lostIndexReply) Upsert(ctx context.Context, points []qdrant.Point) error {
	if err := b.Store.Upsert(ctx, points); err != nil {
		return err
	}
	if b.fail {
		b.fail = false
		return errors.New("injected lost reply after durable upsert")
	}
	return nil
}

func TestInitialIndexPublicationAgainstStores(t *testing.T) {
	for _, physical := range []bool{false, true} {
		t.Run(fmt.Sprintf("content-addressed=%v", physical), func(t *testing.T) {
			t.Run("isolated Qdrant publication", func(t *testing.T) { runInitialIndexPublication(t, false, physical, "") })
			t.Run("missing graph receipt blocks publication", func(t *testing.T) { runInitialIndexPublication(t, true, physical, "") })
		})
	}
}

func runInitialIndexPublication(t *testing.T, requireGraph, contentAddressed bool, storageMode string) {
	dsn, endpoint := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN"), os.Getenv("REGULAGRAPH_TEST_QDRANT_ENDPOINT")
	if storageMode != "" && endpoint == "" {
		endpoint = "http://127.0.0.1:6333" // routing metadata only; no backend calls
	}
	if dsn == "" || endpoint == "" {
		t.Skip("disposable PostgreSQL and Qdrant required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// Existing adapter suites truncate their default schema. Give this package
	// its own schema so `go test ./...` cannot erase an in-flight writer fixture.
	schema := fmt.Sprintf("index_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if _, e := conn.Exec(cleanup, "DROP SCHEMA "+quotedSchema+" CASCADE"); e != nil {
			t.Error(e)
		}
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("integration test requires PostgreSQL URI DSN")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	repo, err := postgres.Open(ctx, postgres.Config{DSN: parsed.String(), MaxConnections: 6, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	dir, _ := filepath.Abs("../../../../migrations")
	if err = repo.ApplyMigrations(ctx, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	corpus := fmt.Sprintf("corpus:initial:%d", time.Now().UnixNano())
	pub := "publication:" + corpus
	reservation, err := repo.ReservePublication(ctx, pub, "", corpus, "snapshot:"+corpus, "")
	if err != nil {
		t.Fatal(err)
	}
	artifacts := indexMemoryArtifacts{}
	putRaw := func(id, media string, raw []byte) *pb.ArtifactRef {
		t.Helper()
		sum := sha256.Sum256(raw)
		ref := &pb.ArtifactRef{ArtifactId: id, SchemaVersion: 1, MediaType: media, ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: fmt.Sprintf("%x", sum)}, StorageKey: fmt.Sprintf("objects/%x", sum)}
		if e := repo.RegisterArtifact(ctx, corpus, ref); e != nil {
			t.Fatal(e)
		}
		artifacts[id] = raw
		return ref
	}
	put := func(id, media string, m proto.Message) *pb.ArtifactRef {
		t.Helper()
		raw, e := proto.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		if contentAddressed {
			var namespace string
			switch m.(type) {
			case *pb.DocumentBatch:
				namespace = "document-batch"
			case *pb.IndexBatch:
				namespace = "index-batch"
			}
			if namespace != "" {
				id = fmt.Sprintf("artifact:%s:%x", namespace, sha256.Sum256(raw))
			}
		}
		return putRaw(id, media, raw)
	}
	load := func(name string, m proto.Message) {
		t.Helper()
		raw, e := os.ReadFile("../../../../tests/fixtures/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if e = proto.Unmarshal(raw, m); e != nil {
			t.Fatal(e)
		}
	}
	var fixtures struct {
		Cases []struct {
			Name  string
			Value json.RawMessage
		}
	}
	raw, err := os.ReadFile("../../../../tests/fixtures/wire-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	plan, batch, source := new(pb.IndexBuildPlan), new(pb.IndexBatch), new(pb.DocumentBatch)
	for _, c := range fixtures.Cases {
		if c.Name == "index-build-plan-valid" {
			if err = protojson.Unmarshal(c.Value, plan); err != nil {
				t.Fatal(err)
			}
		}
		if c.Name == "index-build-batch-valid" {
			if err = protojson.Unmarshal(c.Value, batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	load("index-source-v1.pb", source)
	suffix := fmt.Sprintf(":test:%d", time.Now().UnixNano())
	source.Meta.RecordId += suffix
	source.DependencyManifest.ArtifactId = source.Meta.RecordId
	plan.Meta.RecordId += suffix
	plan.OutputBatchId += suffix
	batch.Meta.RecordId = plan.OutputBatchId
	batch.Dependencies.ArtifactId = plan.OutputBatchId
	for _, m := range []proto.Message{plan, batch, source} {
		rebindIndexFixture(m.ProtoReflect(), corpus, reservation.Sequence)
	}
	snapshot := proto.Clone(plan.TargetSnapshot).(*pb.SnapshotRef)
	snapshot.SnapshotId = reservation.SnapshotID
	snapshot.Sequence = reservation.Sequence
	plan.TargetSnapshot, plan.SourceSnapshot = proto.Clone(snapshot).(*pb.SnapshotRef), proto.Clone(snapshot).(*pb.SnapshotRef)
	source.Context.SnapshotRef = proto.Clone(snapshot).(*pb.SnapshotRef)
	if storageMode == "source-binding" || storageMode == "snapshot-preparation" {
		source.Context.SnapshotRef = nil
	}
	batch.Context.SnapshotRef = proto.Clone(snapshot).(*pb.SnapshotRef)
	// The declared test source has exactly the two selected chunks, with the
	// complete document/version/structure closure retained for provenance checks.
	selected := map[string]bool{}
	for _, item := range plan.Items {
		selected[item.ChunkId] = true
	}
	chunks := source.Chunks[:0]
	for _, chunk := range source.Chunks {
		if selected[chunk.Meta.RecordId] {
			chunks = append(chunks, chunk)
		}
	}
	source.Chunks = chunks
	if storageMode == "wide-output" {
		// Exercise the production 128 x 1024 shape with synthetic unit vectors;
		// each selected chunk keeps valid source closure, with distinct identities.
		chunkTemplate := proto.Clone(source.Chunks[0]).(*pb.Chunk)
		recordTemplate := proto.Clone(batch.Records[0]).(*pb.IndexRecord)
		source.Chunks, batch.Records, plan.Items = nil, nil, nil
		dimensions := uint32(1024)
		plan.Generation.DenseManifest.Dimensions = &dimensions
		for i := range 128 {
			chunk := proto.Clone(chunkTemplate).(*pb.Chunk)
			chunk.Meta.RecordId = fmt.Sprintf("chunk:wide:%d", i)
			record := proto.Clone(recordTemplate).(*pb.IndexRecord)
			record.Meta.RecordId = fmt.Sprintf("record:wide:%d", i)
			record.Dependencies.ArtifactId = record.Meta.RecordId
			record.ChunkId = chunk.Meta.RecordId
			record.DenseVector.Dimensions = dimensions
			record.DenseVector.Values = make([]float32, dimensions)
			record.DenseVector.Values[0] = 1
			source.Chunks = append(source.Chunks, chunk)
			batch.Records = append(batch.Records, record)
			plan.Items = append(plan.Items, &pb.IndexBuildItem{ChunkId: chunk.Meta.RecordId, RecordId: record.Meta.RecordId})
		}
		batch.Counts.Expected, batch.Counts.Accepted = 128, 128
	}
	// Persist bounded synthetic text through the same registry/hash boundary as
	// production hydration. Spans stay fixed; model/text quality is not asserted.
	for _, text := range source.TextArtifacts {
		old := text.NormalizedTextRef
		data := []byte(strings.Repeat("a", int(old.ByteSize)))
		copy(data, []byte("Perizinan usaha memerlukan bukti."))
		text.NormalizedTextRef = putRaw(old.ArtifactId+suffix, old.MediaType, data)
		for _, version := range source.Versions {
			if proto.Equal(version.TextRef, old) {
				version.TextRef = proto.Clone(text.NormalizedTextRef).(*pb.ArtifactRef)
			}
		}
	}
	for i, blob := range source.Sources {
		source.Observations = append(source.Observations, &pb.SourceObservation{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: fmt.Sprintf("observation:%d%s", i, suffix)}, PortalId: "fixture", DetailUrl: "https://example.org/fixture.pdf", ResolvedUrl: "https://example.org/fixture.pdf", FetchedAt: timestamppb.Now(), Status: pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE, SourceBlobId: proto.String(blob.Meta.RecordId)})
	}
	plan.DocumentBatch = put(source.Meta.RecordId, "application/x-protobuf; message=regulagraph.v1.DocumentBatch", source)
	job := "job:" + corpus
	checkpoint := &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "checkpoint:" + corpus}, JobId: job, Stage: pb.JobStage_JOB_STAGE_CHUNK,
		CompletedBatchKeys: []string{plan.DocumentBatch.ArtifactId}, ArtifactHashes: []*pb.ContentHash{plan.DocumentBatch.ContentHash}, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	// Fill producer/attempt fields from the generated contract, then validate.
	checkpoint.Fence = 1
	checkpoint.Manifest = plan.Producer
	if err = domain.ValidateWire(checkpoint, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	checkpointRaw, _ := proto.Marshal(checkpoint)
	checkpointSum := sha256.Sum256(checkpointRaw)
	request := &pb.IngestionRequest{CorpusId: corpus, Operation: pb.JobOperation_JOB_OPERATION_INGEST, IdempotencyKey: job,
		Sources: []*pb.SourceLocator{{PortalId: "bpk", Locator: &pb.SourceLocator_Url{Url: "https://example.test/source.pdf"}}}, ConfigManifest: plan.Producer}
	requestBytes, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(requestBytes))
	_, err = conn.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload) VALUES($1,$2,1,3,$3,$4,$1,$4,$5)`, job, corpus, int16(pb.JobStage_JOB_STAGE_CHUNK), hash, requestBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,1,$4,$5,$6)`, checkpoint.Meta.RecordId, job, int16(checkpoint.Stage), checkpointRaw, fmt.Sprintf("%x", checkpointSum), int16(checkpoint.TerminalStatus))
	if err != nil {
		t.Fatal(err)
	}
	analyzer := new(pb.LexicalAnalyzerArtifact)
	if storageMode == "snapshot-preparation" {
		if err = repo.AbortPublication(ctx, pub); err != nil {
			t.Fatal(err)
		}
		checkInitialSnapshotPreparation(t, ctx, repo, artifacts, corpus, pub+":prepared", plan.Generation.Meta.RecordId, source.Context.AuthScopeRef, job, plan.DocumentBatch)
		return
	}
	if storageMode == "source-binding" {
		boundRef, boundSource := checkSnapshotSourceBinding(t, ctx, repo, conn, pub, reservation.Fence, snapshot, job, plan.DocumentBatch, artifacts, source.Context.AuthScopeRef)
		plan.DocumentBatch, source = boundRef, boundSource
	}
	load("lexical-analyzer-artifact-v1.pb", analyzer)
	analyzer.Meta.RecordId += suffix
	rebindIndexFixture(analyzer.ProtoReflect(), corpus, reservation.Sequence)
	analyzerRef := put(analyzer.Meta.RecordId, "application/x-protobuf; message=regulagraph.v1.LexicalAnalyzerArtifact", analyzer)
	_, revision, err := repo.AllocateLexicalTerms(ctx, corpus, analyzer.AnalyzerId, "operation:seed", 1, []string{"izin", "pasal"})
	if err != nil {
		t.Fatal(err)
	}
	dictionary, err := repo.ExportLexicalDictionary(ctx, corpus, analyzer.AnalyzerId, "dictionary:initial"+suffix, revision, 100, domain.DefaultWireLimits)
	if err != nil {
		t.Fatal(err)
	}
	dictionaryRef := put(dictionary.Meta.RecordId, "application/x-protobuf; message=regulagraph.v1.LexicalDictionaryArtifact", dictionary)
	stats := new(pb.LexicalStatisticsArtifact)
	load("lexical-statistics-v1.pb", stats)
	stats.Meta.RecordId += suffix
	rebindIndexFixture(stats.ProtoReflect(), corpus, reservation.Sequence)
	stats.PopulationSnapshot = proto.Clone(snapshot).(*pb.SnapshotRef)
	stats.DocumentCount = uint64(len(source.Chunks))
	if storageMode == "wide-output" {
		stats.TotalTokens = stats.DocumentCount * 20
		for _, frequency := range stats.DocumentFrequencies {
			frequency.DocumentCount = stats.DocumentCount
		}
	}
	stats.ZeroTokenDocuments = 0
	stats.InputPolicy = plan.LexicalInputPolicy
	statsRef := put(stats.Meta.RecordId, "application/x-protobuf; message=regulagraph.v1.LexicalStatisticsArtifact", stats)
	plan.Generation.LexicalAnalyzer, plan.Generation.LexicalDictionary, plan.Generation.LexicalStatistics = analyzerRef, dictionaryRef, statsRef
	plan.DictionaryChain = []*pb.ArtifactRef{dictionaryRef}
	batch.Generation = proto.Clone(plan.Generation).(*pb.IndexGeneration)
	for _, record := range batch.Records {
		record.SparseVector = &pb.SparseVector{Indices: []uint32{1, 2}, Values: []float32{1, 0.5}}
	}
	seal := func() *pb.ArtifactRef {
		t.Helper()
		ref := put(plan.Meta.RecordId, domain.IndexBuildPlanMediaType, plan)
		batch.BuildPlan = ref
		deps := append([]*pb.DependencyManifest{batch.Dependencies}, func() []*pb.DependencyManifest {
			out := []*pb.DependencyManifest{}
			for _, r := range batch.Records {
				out = append(out, r.Dependencies)
			}
			return out
		}()...)
		for _, dep := range deps {
			dep.Dependencies[0].DependencyId = ref.ArtifactId
			dep.Dependencies[0].Fingerprint = ref.ContentHash
		}
		batch.OperationsChecksum.Sha256, err = domain.IndexPlanOperationsChecksum(batch)
		if err != nil {
			t.Fatal(err)
		}
		return put(batch.Meta.RecordId, domain.IndexBatchMediaType, batch)
	}
	batchRef := seal()
	if contentAddressed && (batchRef.ArtifactId == batch.Meta.RecordId || plan.DocumentBatch.ArtifactId == source.Meta.RecordId) {
		t.Fatal("fixture collapsed physical and logical batch identities")
	}
	binding := domain.IndexCatalogBinding{PublicationID: pub, Fence: reservation.Fence, Endpoint: endpoint, Collection: fmt.Sprintf("initial_%d", time.Now().UnixNano()), Generation: plan.Generation}
	input := []InitialIndexInput{{SourceJobID: job, BatchRef: batchRef}}
	p, err := PrepareInitialIndex(ctx, repo, artifacts, binding, input)
	if err != nil {
		t.Fatal(err)
	}
	if p.ExpectedBackend().ExpectedCounts.Expected != uint64(len(source.Chunks)) {
		t.Fatal("prepared count")
	}
	// The coordinator now constructs the selection, persists its real plan bytes,
	// and admits the exact resulting set before the existing publication path.
	chunksPerBatch := 128
	if storageMode == "inventory" {
		chunksPerBatch = 1
	}
	planned, err := PlanInitialIndex(ctx, repo, artifacts, InitialIndexPlanConfig{
		Binding: binding, Snapshot: snapshot, Producer: plan.Producer,
		DictionaryChain: plan.DictionaryChain, AuthScope: source.Context.AuthScopeRef, ChunksPerBatch: chunksPerBatch,
	}, []InitialIndexSource{{SourceJobID: job, DocumentBatch: plan.DocumentBatch}})
	if err != nil {
		t.Fatal(err)
	}
	planFiles, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer planFiles.Close()
	if err = planned.Persist(ctx, planFiles, repo); err != nil {
		t.Fatal(err)
	}
	if err = planned.Persist(ctx, planFiles, repo); err != nil {
		t.Fatal("plan replay", err)
	}
	if storageMode == "source-binding" {
		return
	}
	if storageMode == "inventory" {
		checkDurableIndexInventory(t, ctx, parsed.String(), conn, planned)
		return
	}
	plannedBatch := planned.Batches()[0]
	planBytes, err := planFiles.ReadVerified(ctx, plannedBatch.Reference, plannedBatch.Reference.ByteSize)
	if err != nil {
		t.Fatal(err)
	}
	artifacts[plannedBatch.Reference.ArtifactId] = planBytes
	generated := proto.Clone(batch).(*pb.IndexBatch)
	generated.Meta.RecordId = plannedBatch.Plan.OutputBatchId
	generated.BuildPlan = plannedBatch.Reference
	generated.Dependencies = &pb.DependencyManifest{ArtifactId: generated.Meta.RecordId, ProducerManifest: plannedBatch.Plan.Producer,
		Dependencies: []*pb.Dependency{{DependencyId: plannedBatch.Reference.ArtifactId, Fingerprint: plannedBatch.Reference.ContentHash}}}
	byChunk := map[string]*pb.IndexRecord{}
	for _, record := range generated.Records {
		byChunk[record.ChunkId] = record
	}
	generated.Records = nil
	for _, item := range plannedBatch.Plan.Items {
		record := byChunk[item.ChunkId]
		if record == nil {
			t.Fatal("planned fixture chunk absent")
		}
		record.Meta.RecordId = item.RecordId
		record.Dependencies = proto.Clone(generated.Dependencies).(*pb.DependencyManifest)
		record.Dependencies.ArtifactId = item.RecordId
		generated.Records = append(generated.Records, record)
	}
	generated.OperationsChecksum.Sha256, err = domain.IndexPlanOperationsChecksum(generated)
	if err != nil {
		t.Fatal(err)
	}
	generatedRef := put(generated.Meta.RecordId, domain.IndexBatchMediaType, generated)
	if storageMode == "processor" {
		checkIndexProcessor(t, ctx, repo, conn, planned, artifacts, generated, true)
		return
	}
	if storageMode == "output" || storageMode == "wide-output" {
		checkIndexOutputCommit(t, ctx, repo, conn, planned, generatedRef, generated)
		return
	}
	checkIndexProcessor(t, ctx, repo, conn, planned, artifacts, generated, false)
	p, err = PrepareCompletedInitialIndex(ctx, repo, artifacts, pub)
	if err != nil {
		t.Fatal("planned output admission", err)
	}
	if _, e := planned.PrepareOutputs(ctx, repo, artifacts, []*pb.ArtifactRef{batchRef}); e == nil {
		t.Fatal("locally valid output from an unselected plan admitted")
	}
	// A registered physical address cannot authorize a different logical output.
	// Keep internal dependency ownership consistent so the immutable plan, not
	// merely a malformed batch, must reject the substituted identity.
	unplanned := proto.Clone(batch).(*pb.IndexBatch)
	unplanned.Meta.RecordId += "-unplanned"
	unplanned.Dependencies.ArtifactId = unplanned.Meta.RecordId
	unplannedRef := put(unplanned.Meta.RecordId, domain.IndexBatchMediaType, unplanned)
	if _, e := PrepareInitialIndex(ctx, repo, artifacts, binding, []InitialIndexInput{{SourceJobID: job, BatchRef: unplannedRef}}); e == nil || !strings.Contains(e.Error(), "INDEX output differs from immutable plan/source") {
		t.Fatal("unplanned logical batch identity was not rejected by plan admission", e)
	}
	// Corrupt bytes must fail even when the reader incorrectly claims verification.
	saved := artifacts[batchRef.ArtifactId]
	artifacts[batchRef.ArtifactId] = []byte("corrupt")
	if _, e := PrepareInitialIndex(ctx, repo, artifacts, binding, input); e == nil {
		t.Fatal("corrupt artifact admitted")
	}
	artifacts[batchRef.ArtifactId] = saved
	if _, e := PrepareInitialIndex(ctx, repo, artifacts, binding, append(input, input...)); e == nil {
		t.Fatal("duplicate batch admitted")
	}
	if _, e := PrepareInitialIndex(ctx, repo, artifacts, binding, []InitialIndexInput{{SourceJobID: "job:wrong", BatchRef: batchRef}}); e == nil {
		t.Fatal("unauthorized source admitted")
	}
	// Self-consistent plan/checksum still cannot omit a declared source chunk.
	plan.Meta.RecordId += "-partial"
	plan.OutputBatchId += "-partial"
	batch.Meta.RecordId = plan.OutputBatchId
	batch.Dependencies.ArtifactId = plan.OutputBatchId
	plan.Items = plan.Items[:1]
	batch.Records = batch.Records[:1]
	batch.Counts.Expected = 1
	batch.Counts.Accepted = 1
	partial := seal()
	if _, e := PrepareInitialIndex(ctx, repo, artifacts, binding, []InitialIndexInput{{SourceJobID: job, BatchRef: partial}}); e == nil {
		t.Fatal("partial initial source admitted")
	}
	physical, err := qdrant.New(endpoint, "", &http.Client{Timeout: 5 * time.Second}, qdrant.Binding{Collection: binding.Collection, CorpusID: corpus, Generation: binding.Generation})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, endpoint+"/collections/"+binding.Collection, nil)
		response, e := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if e == nil {
			response.Body.Close()
		} else {
			t.Error(e)
		}
	}()
	manifest := &pb.PublicationManifest{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: pub}, SnapshotRef: snapshot, Fence: reservation.Fence,
		BackendGenerations: []*pb.BackendGeneration{p.ExpectedBackend()}, ValidationReport: &pb.ValidationReport{Valid: true, CheckedRecords: uint64(len(p.records))}}
	if requireGraph {
		graph := proto.Clone(p.ExpectedBackend()).(*pb.BackendGeneration)
		graph.Backend = pb.BackendKind_BACKEND_KIND_NEO4J
		graph.Generation = "graph:required"
		manifest.BackendGenerations = append(manifest.BackendGenerations, graph)
	}
	coordinator, _ := NewPublicationCoordinator(repo)
	if err = coordinator.Stage(ctx, reservation, manifest); err != nil {
		t.Fatal(err)
	}
	backend := &lostIndexReply{Store: physical, fail: true}
	release, err := repo.AcquireIndexWriteLock(ctx, pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AcquireIndexWriteLock(ctx, pub); !errors.Is(err, domain.ErrLeaseUnavailable) {
		t.Fatalf("concurrent writer admitted: %v", err)
	}
	release()
	release()
	changed := *p
	changed.digest = strings.Repeat("b", 64)
	if err = WriteInitialIndex(ctx, repo, backend, &changed); err == nil {
		t.Fatal("unstaged write set accepted")
	}
	if err = WriteInitialIndex(ctx, repo, backend, p); err == nil {
		t.Fatal("lost write response accepted")
	}
	if err = coordinator.Publish(ctx, pub); err == nil {
		t.Fatal("partial write published")
	}
	if err = coordinator.Abort(ctx, pub); err == nil {
		t.Fatal("uncompensated write aborted")
	}
	for range 2 {
		if err = WriteInitialIndex(ctx, repo, backend, p); err != nil {
			t.Fatal(err)
		}
	}
	if requireGraph {
		if err = coordinator.Publish(ctx, pub); err == nil {
			t.Fatal("Qdrant receipt substituted for graph readiness")
		}
		if _, err = PublishCompletedVectorIndex(ctx, repo, artifacts, physical, pub); err == nil {
			t.Fatal("vector publisher removed staged graph requirement")
		}
		return
	}
	inventory, e := planned.JobInventory()
	if e != nil {
		t.Fatal(e)
	}
	for _, cancelledJob := range []string{job, inventory.Assignments[0].JobID} {
		if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=true WHERE job_id=$1`, cancelledJob); err != nil {
			t.Fatal(err)
		}
		if err = coordinator.Publish(ctx, pub); !errors.Is(err, postgres.ErrPublicationNotReady) {
			t.Fatal("late cancellation published", err)
		}
		if _, err = conn.Exec(ctx, `UPDATE jobs SET cancellation_requested=false WHERE job_id=$1`, cancelledJob); err != nil {
			t.Fatal(err)
		}
	}
	// Publication must back off, not deadlock, while a checkpoint/cancellation
	// transaction holds a child lock and may next request the publication lock.
	blocker, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `SELECT job_id FROM jobs WHERE job_id=$1 FOR UPDATE`, inventory.Assignments[0].JobID); err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Publish(ctx, pub); !errors.Is(err, postgres.ErrPublicationNotReady) {
		t.Fatal("locked child did not block publication", err)
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		published, e := PublishCompletedVectorIndex(ctx, repo, artifacts, physical, pub)
		if e != nil || !proto.Equal(published, snapshot) {
			t.Fatal("completed inventory publication/replay", e)
		}
	}
	if err = coordinator.Publish(ctx, pub); err != nil {
		t.Fatal(err)
	}
	if err = WriteInitialIndex(ctx, repo, backend, p); err == nil {
		t.Fatal("terminal snapshot accepted fresh writes")
	}
	verifyPublishedHydration(t, ctx, repo, physical, p, artifacts, source)
	verifyPublishedRAG(t, ctx, repo, physical, p, artifacts, source.DependencyManifest.ProducerManifest)
}

func verifyPublishedHydration(t *testing.T, ctx context.Context, repo *postgres.Repository, physical *qdrant.Store, p *PreparedInitialIndex, artifacts indexMemoryArtifacts, source *pb.DocumentBatch) {
	t.Helper()
	pin, err := repo.PinActiveSnapshot(ctx, p.snapshot.CorpusId, "read:"+p.binding.PublicationID, "reader:fixture", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), pin.LeaseID, pin.OwnerID)
	index, err := repo.LoadPinnedIndex(ctx, pin)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(index.Snapshot, p.snapshot) || !proto.Equal(index.Binding.Generation, p.binding.Generation) {
		t.Fatal("read admitted a different snapshot generation")
	}
	ids := []string{p.records[1].Meta.RecordId, p.records[0].Meta.RecordId}
	read, err := repo.LoadPinnedIndexRecords(ctx, pin, ids, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	if read[0].Record.Meta.RecordId != ids[0] || read[1].Record.Meta.RecordId != ids[1] {
		t.Fatal("catalog lost caller order")
	}
	for _, bad := range [][]string{{ids[0], ids[0]}, {ids[0], "index:absent"}} {
		if _, err = repo.LoadPinnedIndexRecords(ctx, pin, bad, 4<<20); err == nil {
			t.Fatal("invalid catalog selection returned partial success")
		}
	}
	if _, err = repo.LoadPinnedIndexRecords(ctx, pin, ids, 1); err == nil {
		t.Fatal("catalog exceeded requested byte budget")
	}
	wrong := pin
	wrong.OwnerID = "reader:wrong"
	if _, err = repo.LoadPinnedIndex(ctx, wrong); err == nil {
		t.Fatal("foreign lease owner accepted")
	}
	wrong = pin
	wrong.Sequence++
	if _, err = repo.LoadPinnedIndex(ctx, wrong); err == nil {
		t.Fatal("forged snapshot sequence accepted")
	}
	h, err := retrieval.NewSourceHydrator(repo, artifacts, index, retrieval.HydrationConfig{MaximumCandidates: 32, MaximumArtifactBytes: 16 << 20, MaximumEvidenceBytes: 1 << 20, Producer: source.DependencyManifest.ProducerManifest})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := physical.SearchDense(ctx, p.records[0].DenseVector.Values, qdrant.SearchScope{SnapshotSeq: pin.Sequence, Limit: 2})
	if err != nil || len(hits) != 2 {
		t.Fatal(hits, err)
	}
	request := &pb.QuestionRequest{Question: "Apa ketentuannya?", CorpusId: pin.CorpusID, ResponseMode: pb.ResponseMode_RESPONSE_MODE_COMPLETE, RequestedProfile: pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG,
		TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_AS_OF, EffectiveAt: &pb.CalendarDate{Year: 2026, Month: 1, Day: 1}, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}
	result, err := h.Hydrate(ctx, request, hits)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence.Items) != 2 || !proto.Equal(result.Evidence.Snapshot, p.snapshot) || result.Evidence.Completeness != pb.Completeness_COMPLETENESS_PARTIAL {
		t.Fatal("hydration lost evidence or hid unresolved dates/context")
	}
	limited, err := retrieval.NewSourceHydrator(repo, artifacts, index, retrieval.HydrationConfig{MaximumCandidates: 32, MaximumArtifactBytes: 16 << 20, MaximumEvidenceBytes: uint64(proto.Size(result.Evidence) - 1), Producer: source.DependencyManifest.ProducerManifest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = limited.Hydrate(ctx, request, hits); err == nil {
		t.Fatal("bundle metadata escaped total output budget")
	}
	for _, evidence := range result.Evidence.Items {
		span := evidence.SourceSpans[0]
		for _, text := range source.TextArtifacts {
			if text.Meta.RecordId == span.TextArtifactId && evidence.Text != string(artifacts[text.NormalizedTextRef.ArtifactId][span.StartByte:span.EndByte]) {
				t.Fatal("evidence text did not come from verified artifact span")
			}
		}
		for _, ref := range evidence.SourceRefs {
			urls, e := result.SourceURLs(ref.SourceBlobId, ref.ProvisionVersionId)
			if e != nil || len(urls) != 1 || urls[0] != "https://example.org/fixture.pdf" {
				t.Fatal(urls, e)
			}
		}
	}
	forged := append([]qdrant.Hit(nil), hits...)
	forged[0].PointID = "00000000-0000-8000-8000-000000000000"
	if _, err = h.Hydrate(ctx, request, forged); err == nil {
		t.Fatal("foreign Qdrant point reached evidence")
	}
	request.TemporalScope.UnresolvedPolicy = pb.UnresolvedPolicy_UNRESOLVED_POLICY_EXCLUDE
	excluded, err := h.Hydrate(ctx, request, hits)
	if err != nil || len(excluded.Rejected) != len(hits) || len(excluded.Evidence.Items) != 0 {
		t.Fatal("unresolved exclusion", err)
	}
	request.TemporalScope.UnresolvedPolicy = pb.UnresolvedPolicy_UNRESOLVED_POLICY_REQUIRE_REVIEW
	if _, err = h.Hydrate(ctx, request, hits); err == nil {
		t.Fatal("required legal review bypassed")
	}
	request.TemporalScope.UnresolvedPolicy = pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT
	textRef := source.TextArtifacts[0].NormalizedTextRef
	saved := artifacts[textRef.ArtifactId]
	artifacts[textRef.ArtifactId] = []byte("corrupt")
	if _, err = h.Hydrate(ctx, request, hits); err == nil {
		t.Fatal("corrupt source text reached evidence")
	}
	artifacts[textRef.ArtifactId] = saved
	if err = repo.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Hydrate(ctx, request, hits); err == nil {
		t.Fatal("released snapshot lease still served evidence")
	}
}

func rebindIndexFixture(m protoreflect.Message, corpus string, sequence uint64) {
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.Kind() == protoreflect.StringKind && f.Name() == "corpus_id" {
			m.Set(f, protoreflect.ValueOfString(corpus))
		}
		if f.Kind() == protoreflect.Uint64Kind && f.Name() == "from_seq" {
			m.Set(f, protoreflect.ValueOfUint64(sequence))
		}
		if f.Kind() == protoreflect.MessageKind {
			if f.IsList() {
				list := v.List()
				for i := 0; i < list.Len(); i++ {
					rebindIndexFixture(list.Get(i).Message(), corpus, sequence)
				}
			} else if !f.IsMap() {
				rebindIndexFixture(v.Message(), corpus, sequence)
			}
		}
		return true
	})
}
