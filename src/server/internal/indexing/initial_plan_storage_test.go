// Verifies immutable plan persistence using the real confined file store, with
// injected registry failure and replay. Plan admission is covered independently;
// these tests assert no successful handoff before every dependency is registered.
// Synthetic records and local file I/O are not performance/quality acceptance.
package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/domain"
)

type planTestRegistry struct {
	refs map[string]*pb.ArtifactRef
	deps map[string]*pb.DependencyManifest
	fail bool
}

func (r *planTestRegistry) RegisterArtifact(_ context.Context, _ string, ref *pb.ArtifactRef) error {
	if old := r.refs[ref.ArtifactId]; old != nil && !proto.Equal(old, ref) {
		return errors.New("immutable registration conflict")
	}
	r.refs[ref.ArtifactId] = proto.Clone(ref).(*pb.ArtifactRef)
	return nil
}
func (r *planTestRegistry) ReplaceArtifactDependencyManifest(_ context.Context, _ string, id string, dep *pb.DependencyManifest) error {
	if r.fail {
		return errors.New("injected registry outage")
	}
	if err := domain.ValidateWire(dep, domain.DefaultWireLimits); err != nil {
		return err
	}
	r.deps[id] = proto.Clone(dep).(*pb.DependencyManifest)
	return nil
}
func TestInitialPlansPersistAndRepairInterruptedRegistration(t *testing.T) {
	f := newPlanningFixture(t)
	p, err := PlanInitialIndex(context.Background(), f.authority, f.artifacts, f.config, f.inputs)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := &planTestRegistry{refs: map[string]*pb.ArtifactRef{}, deps: map[string]*pb.DependencyManifest{}, fail: true}
	if err = p.Persist(context.Background(), store, registry); err == nil {
		t.Fatal("incomplete registration succeeded")
	}
	registry.fail = false
	for range 2 {
		if err = p.Persist(context.Background(), store, registry); err != nil {
			t.Fatal(err)
		}
	}
	if len(registry.refs) != len(p.Batches()) || len(registry.deps) != len(p.Batches()) {
		t.Fatal("incomplete persisted plan set")
	}
	for _, batch := range p.Batches() {
		raw, err := store.ReadVerified(context.Background(), batch.Reference, batch.Reference.ByteSize)
		if err != nil {
			t.Fatal(err)
		}
		decoded := new(pb.IndexBuildPlan)
		if err = domain.DecodeWire(raw, decoded, domain.DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(decoded, batch.Plan) {
			t.Fatal("persisted plan drift")
		}
		dep := registry.deps[batch.Reference.ArtifactId]
		if len(dep.Dependencies) != 4 || !proto.Equal(dep.ProducerManifest, batch.Plan.Producer) {
			t.Fatal("missing transitive source/lexical dependencies")
		}
		refs := []*pb.ArtifactRef{batch.Plan.DocumentBatch, batch.Plan.Generation.LexicalAnalyzer, batch.Plan.Generation.LexicalStatistics, batch.Plan.Generation.LexicalDictionary}
		for i, ref := range refs {
			if dep.Dependencies[i].DependencyId != ref.ArtifactId || !proto.Equal(dep.Dependencies[i].Fingerprint, ref.ContentHash) {
				t.Fatal("dependency hash drift")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = p.Persist(ctx, store, registry); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel lost", err)
	}
}

func TestInitialPlansRejectIncompleteOutputSet(t *testing.T) {
	f := newPlanningFixture(t)
	p, err := PlanInitialIndex(context.Background(), f.authority, f.artifacts, f.config, f.inputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.PrepareOutputs(context.Background(), f.authority, f.artifacts, nil); err == nil {
		t.Fatal("missing outputs accepted")
	}
	refs := make([]*pb.ArtifactRef, len(p.Batches()))
	for i := range refs {
		refs[i] = f.inputs[0].DocumentBatch
	}
	if _, err = p.PrepareOutputs(context.Background(), f.authority, f.artifacts, refs); err == nil {
		t.Fatal("wrong output type accepted")
	}
}

func plannedOutputFixture(t *testing.T) (*planningFixture, *InitialIndexPlans, []*pb.ArtifactRef) {
	t.Helper()
	f := newPlanningFixture(t)
	raw, err := os.ReadFile("../../../../tests/fixtures/wire-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Name  string
			Value json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	template := new(pb.IndexBatch)
	for _, c := range suite.Cases {
		if c.Name == "index-build-batch-valid" {
			if err = protojson.Unmarshal(c.Value, template); err != nil {
				t.Fatal(err)
			}
		}
	}
	rebindIndexFixture(template.ProtoReflect(), f.config.Snapshot.CorpusId, f.config.Snapshot.Sequence)
	byChunk := map[string]*pb.IndexRecord{}
	for _, r := range template.Records {
		byChunk[r.ChunkId] = r
	}
	selected := f.source.Chunks[:0]
	for _, c := range f.source.Chunks {
		if byChunk[c.Meta.RecordId] != nil {
			selected = append(selected, c)
		}
	}
	f.source.Chunks = selected
	f.updateSource(t)
	f.stats.DocumentCount = uint64(len(selected))
	f.stats.TotalTokens = uint64(len(selected)) * 10
	for _, df := range f.stats.DocumentFrequencies {
		df.DocumentCount = uint64(len(selected))
	}
	f.config.Binding.Generation.LexicalStatistics = f.put(t, f.stats, f.stats.Meta.RecordId)
	f.config.ChunksPerBatch = 1
	p, err := PlanInitialIndex(context.Background(), f.authority, f.artifacts, f.config, f.inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Batches()) < 2 {
		t.Fatal("test requires multiple batches")
	}
	outputs := []*pb.ArtifactRef{}
	for _, planned := range p.Batches() {
		_, raw, err := initialPlanArtifact(planned.Plan)
		if err != nil {
			t.Fatal(err)
		}
		f.artifacts[planned.Reference.ArtifactId] = raw
		f.authority.refs[planned.Reference.ArtifactId] = planned.Reference
		batch := proto.Clone(template).(*pb.IndexBatch)
		batch.Meta.RecordId = planned.Plan.OutputBatchId
		batch.Context.SnapshotRef = proto.Clone(f.config.Snapshot).(*pb.SnapshotRef)
		batch.Generation = proto.Clone(planned.Plan.Generation).(*pb.IndexGeneration)
		batch.BuildPlan = planned.Reference
		batch.Counts = &pb.Counts{Expected: 1, Accepted: 1}
		batch.Dependencies = &pb.DependencyManifest{ArtifactId: batch.Meta.RecordId, ProducerManifest: planned.Plan.Producer, Dependencies: []*pb.Dependency{{DependencyId: planned.Reference.ArtifactId, Fingerprint: planned.Reference.ContentHash}}}
		item := planned.Plan.Items[0]
		record := proto.Clone(byChunk[item.ChunkId]).(*pb.IndexRecord)
		record.Meta.RecordId = item.RecordId
		record.SparseVector = &pb.SparseVector{Indices: []uint32{1, 2}, Values: []float32{1, 0.5}}
		record.Dependencies = proto.Clone(batch.Dependencies).(*pb.DependencyManifest)
		record.Dependencies.ArtifactId = item.RecordId
		batch.Records = []*pb.IndexRecord{record}
		batch.OperationsChecksum.Sha256, err = domain.IndexPlanOperationsChecksum(batch)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, f.put(t, batch, "physical:"+batch.Meta.RecordId))
	}
	return f, p, outputs
}

func TestInitialPlansAdmitMultipleBatchesAndRejectDuplicatePlan(t *testing.T) {
	f, p, outputs := plannedOutputFixture(t)
	prepared, err := p.PrepareOutputs(context.Background(), f.authority, f.artifacts, outputs)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ExpectedBackend().ExpectedCounts.Expected != uint64(len(f.source.Chunks)) {
		t.Fatal("incomplete output coverage")
	}
	// Delivery order can change; identity and write commitment cannot.
	for i, j := 0, len(outputs)-1; i < j; i, j = i+1, j-1 {
		outputs[i], outputs[j] = outputs[j], outputs[i]
	}
	replayed, err := p.PrepareOutputs(context.Background(), f.authority, f.artifacts, outputs)
	if err != nil || !proto.Equal(prepared.ExpectedBackend(), replayed.ExpectedBackend()) {
		t.Fatal("reordered delivery drift", err)
	}
	outputs[1] = outputs[0]
	if _, err = p.PrepareOutputs(context.Background(), f.authority, f.artifacts, outputs); err == nil {
		t.Fatal("duplicate plan accepted despite missing sibling")
	}
}
