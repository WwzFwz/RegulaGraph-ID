// Exercises complete-inventory planning with the shared Rust-produced document
// fixture and registered lexical artifacts. Tests cover deterministic partitioning,
// input/output ownership and rejection before inference or writes. Authority is
// synthetic here; PostgreSQL authority/publication have separate integration tests.
package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type planningAuthority struct {
	refs            map[string]*pb.ArtifactRef
	sources         map[string]*pb.ArtifactRef
	dictionaryError bool
}

func (a *planningAuthority) LoadArtifact(_ context.Context, _ string, id string) (*pb.ArtifactRef, error) {
	r := a.refs[id]
	if r == nil {
		return nil, errors.New("unregistered")
	}
	return proto.Clone(r).(*pb.ArtifactRef), nil
}
func (a *planningAuthority) VerifyIndexSourceCheckpoint(_ context.Context, _ string, job string, ref *pb.ArtifactRef) error {
	if !proto.Equal(a.sources[job], ref) {
		return errors.New("checkpoint mismatch")
	}
	return nil
}
func (a *planningAuthority) VerifyIndexDictionary(context.Context, string, *pb.LexicalDictionaryArtifact) error {
	if a.dictionaryError {
		return errors.New("registry mismatch")
	}
	return nil
}

type planningFixture struct {
	config    InitialIndexPlanConfig
	inputs    []InitialIndexSource
	authority *planningAuthority
	artifacts indexMemoryArtifacts
	source    *pb.DocumentBatch
	stats     *pb.LexicalStatisticsArtifact
}

func newPlanningFixture(t *testing.T) *planningFixture {
	t.Helper()
	f := &planningFixture{authority: &planningAuthority{refs: map[string]*pb.ArtifactRef{}, sources: map[string]*pb.ArtifactRef{}}, artifacts: indexMemoryArtifacts{}}
	read := func(name string, target proto.Message) {
		raw, err := os.ReadFile("../../../../tests/fixtures/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err = proto.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}
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
	plan := new(pb.IndexBuildPlan)
	for _, c := range suite.Cases {
		if c.Name == "index-build-plan-valid" {
			if err = protojson.Unmarshal(c.Value, plan); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.source = new(pb.DocumentBatch)
	read("index-source-v1.pb", f.source)
	// The fixture's entire chunk population is the selected inventory. Matching
	// frozen stats are deliberate test input, never a claim of measured BM25 DF.
	f.source.Context.SnapshotRef = proto.Clone(plan.TargetSnapshot).(*pb.SnapshotRef)
	analyzer := new(pb.LexicalAnalyzerArtifact)
	read("lexical-analyzer-artifact-v1.pb", analyzer)
	dictionary := new(pb.LexicalDictionaryArtifact)
	read("lexical-dictionary-v1.pb", dictionary)
	f.stats = new(pb.LexicalStatisticsArtifact)
	read("lexical-statistics-v1.pb", f.stats)
	for _, message := range []proto.Message{f.source, analyzer, dictionary, f.stats} {
		rebindIndexFixture(message.ProtoReflect(), plan.TargetSnapshot.CorpusId, plan.TargetSnapshot.Sequence)
	}
	f.stats.PopulationSnapshot = proto.Clone(plan.TargetSnapshot).(*pb.SnapshotRef)
	f.stats.DocumentCount = uint64(len(f.source.Chunks))
	f.stats.ZeroTokenDocuments = 0
	f.stats.TotalTokens = uint64(len(f.source.Chunks)) * 10
	f.stats.InputPolicy = "structure-labels-v1"
	for _, df := range f.stats.DocumentFrequencies {
		df.DocumentCount = uint64(len(f.source.Chunks))
	}
	plan.Generation.LexicalAnalyzer = f.put(t, analyzer, analyzer.Meta.RecordId)
	plan.Generation.LexicalDictionary = f.put(t, dictionary, dictionary.Meta.RecordId)
	plan.Generation.LexicalStatistics = f.put(t, f.stats, f.stats.Meta.RecordId)
	f.config = InitialIndexPlanConfig{Binding: domain.IndexCatalogBinding{PublicationID: "publication:planned", Fence: 1, Endpoint: "http://127.0.0.1:6333", Collection: "initial_planned", Generation: plan.Generation}, Snapshot: plan.TargetSnapshot, Producer: plan.Producer,
		DictionaryChain: []*pb.ArtifactRef{plan.Generation.LexicalDictionary}, AuthScope: f.source.Context.AuthScopeRef, ChunksPerBatch: 2}
	f.updateSource(t)
	return f
}
func (f *planningFixture) put(t *testing.T, m proto.Message, id string) *pb.ArtifactRef {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	r := &pb.ArtifactRef{ArtifactId: id, SchemaVersion: 1, MediaType: "application/x-protobuf; message=" + string(m.ProtoReflect().Descriptor().FullName()), ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: digest}, StorageKey: "sha256/" + digest[:2] + "/" + digest[2:4] + "/" + digest + ".bin"}
	f.artifacts[id] = raw
	f.authority.refs[id] = r
	return r
}
func (f *planningFixture) updateSource(t *testing.T) {
	r := f.put(t, f.source, "artifact:source:physical")
	f.inputs = []InitialIndexSource{{"job:source", r}}
	f.authority.sources["job:source"] = r
}

func TestInitialIndexPlanningPartitionsAndOwnsInventory(t *testing.T) {
	f := newPlanningFixture(t)
	plan, err := PlanInitialIndex(context.Background(), f.authority, f.artifacts, f.config, f.inputs)
	if err != nil {
		t.Fatal(err)
	}
	batches := plan.Batches()
	if len(batches) != (len(f.source.Chunks)+1)/2 {
		t.Fatal("incomplete partition")
	}
	seen := map[string]bool{}
	for _, b := range batches {
		if err := domain.ValidateIndexBuildPlan(b.Plan); err != nil {
			t.Fatal(err)
		}
		if len(b.Plan.Items) > 2 || b.SourceJobID != "job:source" {
			t.Fatal("invalid partition")
		}
		for _, item := range b.Plan.Items {
			if seen[item.ChunkId] {
				t.Fatal("duplicate chunk")
			}
			seen[item.ChunkId] = true
		}
		ref, raw, err := initialPlanArtifact(b.Plan)
		if err != nil || !proto.Equal(ref, b.Reference) || len(raw) == 0 {
			t.Fatal("unstable plan artifact", err)
		}
	}
	if len(seen) != len(f.source.Chunks) {
		t.Fatal("missing chunks")
	}
	replay, err := PlanInitialIndex(context.Background(), f.authority, f.artifacts, f.config, f.inputs)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range replay.Batches() {
		if !proto.Equal(b.Reference, batches[i].Reference) {
			t.Fatal("replay drift")
		}
	}
	f.config.Binding.Generation.Meta.RecordId = "generation:mutated"
	f.config.Snapshot.Sequence++
	f.inputs[0].DocumentBatch.ArtifactId = "artifact:mutated"
	batches[0].Plan.Items[0].ChunkId = "chunk:mutated"
	batches[0].Plan.Generation.Meta.RecordId = "generation:other"
	batches[0].Reference.ContentHash.Sha256 = "bad"
	for i, b := range plan.Batches() {
		if !proto.Equal(b.Plan, replay.Batches()[i].Plan) || !proto.Equal(b.Reference, replay.Batches()[i].Reference) {
			t.Fatal("caller mutated private plan")
		}
	}
}

func TestInitialIndexPlanningRejectsInvalidInventory(t *testing.T) {
	cases := map[string]func(*planningFixture){
		"empty":            func(f *planningFixture) { f.inputs = nil },
		"duplicate job":    func(f *planningFixture) { f.inputs = append(f.inputs, f.inputs[0]) },
		"unregistered":     func(f *planningFixture) { delete(f.authority.refs, f.inputs[0].DocumentBatch.ArtifactId) },
		"checkpoint drift": func(f *planningFixture) { delete(f.authority.sources, "job:source") },
		"corrupt source":   func(f *planningFixture) { f.artifacts[f.inputs[0].DocumentBatch.ArtifactId] = []byte("corrupt") },
		"scope":            func(f *planningFixture) { f.config.AuthScope = "scope:other" },
		"source snapshot":  func(f *planningFixture) { f.source.Context.SnapshotRef.Sequence++; f.updateSource(t) },
		"partial source": func(f *planningFixture) {
			f.source.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
			f.updateSource(t)
		},
		"population": func(f *planningFixture) {
			f.stats.DocumentCount++
			f.config.Binding.Generation.LexicalStatistics = f.put(t, f.stats, f.stats.Meta.RecordId)
		},
		"policy": func(f *planningFixture) {
			f.stats.InputPolicy = "other"
			f.config.Binding.Generation.LexicalStatistics = f.put(t, f.stats, f.stats.Meta.RecordId)
		},
		"dictionary authority": func(f *planningFixture) { f.authority.dictionaryError = true },
		"batch overflow":       func(f *planningFixture) { f.config.ChunksPerBatch = 129 },
		"nil snapshot":         func(f *planningFixture) { f.config.Snapshot = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPlanningFixture(t)
			mutate(f)
			if p, err := PlanInitialIndex(context.Background(), f.authority, f.artifacts, f.config, f.inputs); err == nil || p != nil {
				t.Fatal("invalid inventory admitted")
			}
		})
	}
	f := newPlanningFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanInitialIndex(ctx, f.authority, f.artifacts, f.config, f.inputs); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel not preserved", err)
	}
}
