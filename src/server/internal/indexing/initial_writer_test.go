// Tests the declared initial snapshot through authenticated Rust-derived source
// fixtures, PostgreSQL authority/intent and real Qdrant. Source job/checkpoint
// rows and normalized vectors are synthetic seeds, not a run of live ingestion
// or inference. Both disposable endpoint env vars are required; skip is not PASS.
// Fault injection proves a lost write reply leaves no publishable partial state.
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
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
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
	t.Run("isolated Qdrant publication", func(t *testing.T) { runInitialIndexPublication(t, false) })
	t.Run("missing graph receipt blocks publication", func(t *testing.T) { runInitialIndexPublication(t, true) })
}

func runInitialIndexPublication(t *testing.T, requireGraph bool) {
	dsn, endpoint := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN"), os.Getenv("REGULAGRAPH_TEST_QDRANT_ENDPOINT")
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
	put := func(id, media string, m proto.Message) *pb.ArtifactRef {
		t.Helper()
		raw, e := proto.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		ref := &pb.ArtifactRef{ArtifactId: id, SchemaVersion: 1, MediaType: media, ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: fmt.Sprintf("%x", sum)}, StorageKey: fmt.Sprintf("objects/%x", sum)}
		if e = repo.RegisterArtifact(ctx, corpus, ref); e != nil {
			t.Fatal(e)
		}
		artifacts[id] = raw
		return ref
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
	hash := strings.Repeat("a", 64)
	_, err = conn.Exec(ctx, `INSERT INTO jobs(job_id,corpus_id,operation,state,stage,input_fingerprint,idempotency_key,request_hash,request_payload) VALUES($1,$2,1,3,$3,$4,$1,$4,$5)`, job, corpus, int16(pb.JobStage_JOB_STAGE_CHUNK), hash, []byte("synthetic test seed"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO job_checkpoints(checkpoint_id,job_id,stage,fence,payload,payload_hash,terminal_status) VALUES($1,$2,$3,1,$4,$5,$6)`, checkpoint.Meta.RecordId, job, int16(checkpoint.Stage), checkpointRaw, fmt.Sprintf("%x", checkpointSum), int16(checkpoint.TerminalStatus))
	if err != nil {
		t.Fatal(err)
	}
	analyzer := new(pb.LexicalAnalyzerArtifact)
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
	binding := domain.IndexCatalogBinding{PublicationID: pub, Fence: reservation.Fence, Endpoint: endpoint, Collection: fmt.Sprintf("initial_%d", time.Now().UnixNano()), Generation: plan.Generation}
	input := []InitialIndexInput{{SourceJobID: job, BatchRef: batchRef}}
	p, err := PrepareInitialIndex(ctx, repo, artifacts, binding, input)
	if err != nil {
		t.Fatal(err)
	}
	if p.ExpectedBackend().ExpectedCounts.Expected != uint64(len(source.Chunks)) {
		t.Fatal("prepared count")
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
		return
	}
	if err = coordinator.Publish(ctx, pub); err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Publish(ctx, pub); err != nil {
		t.Fatal(err)
	}
	if err = WriteInitialIndex(ctx, repo, backend, p); err == nil {
		t.Fatal("terminal snapshot accepted fresh writes")
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
