// Tests exact canonical coverage and ASSEMBLE role/publication pinning with C01
// fixtures. These guards do not authenticate a database or prove graph quality.
package domain

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func graphAssemblyFixture() (*pb.GraphAssemblyPlan, *pb.RegistryEntityView) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	producer := &pb.ProducerManifest{Software: "fixture", Build: "test", SchemaVersion: 1, ConfigHash: hash}
	ref := func(id, media string) *pb.ArtifactRef {
		return &pb.ArtifactRef{ArtifactId: id, ContentHash: hash, StorageKey: "sha256/fixture", SchemaVersion: 1, MediaType: media, ByteSize: 100}
	}
	plan := &pb.GraphAssemblyPlan{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:test", RecordId: "plan:test"},
		Context: &pb.RequestContext{SchemaVersion: 1, CorpusId: "corpus:test", RequestId: "request:test", TraceId: "trace:test", AuthScopeRef: "scope:test", ConfigFingerprint: hash,
			Deadline: &timestamppb.Timestamp{Seconds: 1900000000}, SnapshotRef: &pb.SnapshotRef{CorpusId: "corpus:test", SnapshotId: "snapshot:base", Sequence: 1, ManifestHash: hash, RepresentationGeneration: "representation:base"}},
		PublicationId: "publication:test", PublicationFence: 2, TargetSequence: 2, RegistryRevision: 7,
		DocumentBatch: ref("artifact:document", DocumentBatchMediaType), ExtractionBatch: ref("artifact:extract", ExtractionBatchMediaType),
		ResolutionBatch: ref("artifact:resolve", "application/x-protobuf"), RegistryView: ref("artifact:registry", RegistryEntityViewMediaType),
		OutputArtifactId: "delta:test", SourceCheckpointId: "checkpoint:resolve", ProducerManifest: producer, OntologyHash: hash}
	view := &pb.RegistryEntityView{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:test", RecordId: "artifact:registry"}, PublicationId: plan.PublicationId, PublicationFence: 2, RegistryRevision: 7,
		RequestedIds: []string{"canonical:a", "canonical:b"}, ProducerManifest: producer}
	for _, id := range view.RequestedIds {
		view.Entities = append(view.Entities, &pb.CanonicalEntity{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:test", RecordId: id},
			EntityType: "organization", Scope: "ID:national", PreferredLabel: id, RegistryRevision: 6, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED})
	}
	return plan, view
}

func TestGraphAssemblyPlanAndRegistrySelection(t *testing.T) {
	p, v := graphAssemblyFixture()
	if err := ValidateAssemblyRegistryBinding(p, v); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*pb.RegistryEntityView){
		"missing": func(v *pb.RegistryEntityView) { v.Entities = v.Entities[:1] },
		"extra": func(v *pb.RegistryEntityView) {
			v.Entities = append(v.Entities, proto.Clone(v.Entities[0]).(*pb.CanonicalEntity))
		},
		"order":             func(v *pb.RegistryEntityView) { v.Entities[0], v.Entities[1] = v.Entities[1], v.Entities[0] },
		"duplicate":         func(v *pb.RegistryEntityView) { v.RequestedIds[1] = v.RequestedIds[0] },
		"future":            func(v *pb.RegistryEntityView) { v.Entities[0].RegistryRevision = 8 },
		"corpus":            func(v *pb.RegistryEntityView) { v.Entities[0].Meta.CorpusId = "foreign" },
		"schema":            func(v *pb.RegistryEntityView) { v.Entities[0].Meta.SchemaVersion = 2 },
		"visibility":        func(v *pb.RegistryEntityView) { v.Entities[0].Meta.Visibility = &pb.Visibility{FromSeq: 1} },
		"type":              func(v *pb.RegistryEntityView) { v.Entities[0].EntityType = "invented" },
		"fence":             func(v *pb.RegistryEntityView) { v.PublicationFence++ },
		"artifact identity": func(v *pb.RegistryEntityView) { v.Meta.RecordId = "artifact:other" },
		"unknown root":      func(v *pb.RegistryEntityView) { v.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
		"unknown entity":    func(v *pb.RegistryEntityView) { v.Entities[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := proto.Clone(v).(*pb.RegistryEntityView)
			mutate(copy)
			if ValidateAssemblyRegistryBinding(p, copy) == nil {
				t.Fatal("invalid view accepted")
			}
		})
	}
	v.RequestedIds = nil
	v.Entities = nil
	if err := ValidateRegistryEntityView(v, 1, 1<<20); err != nil {
		t.Fatalf("explicit empty selection: %v", err)
	}
}

func TestGraphAssemblyPlanRejectsDriftAndResourceOverflow(t *testing.T) {
	p, _ := graphAssemblyFixture()
	for name, mutate := range map[string]func(*pb.GraphAssemblyPlan){
		"same snapshot":     func(p *pb.GraphAssemblyPlan) { p.TargetSequence = 1 },
		"overflow":          func(p *pb.GraphAssemblyPlan) { p.TargetSequence = ^uint64(0) },
		"base absent":       func(p *pb.GraphAssemblyPlan) { p.Context.SnapshotRef = nil },
		"role overlap":      func(p *pb.GraphAssemblyPlan) { p.RegistryView.ArtifactId = p.DocumentBatch.ArtifactId },
		"output collision":  func(p *pb.GraphAssemblyPlan) { p.OutputArtifactId = p.ResolutionBatch.ArtifactId },
		"unbounded":         func(p *pb.GraphAssemblyPlan) { p.DocumentBatch.ByteSize = 17 << 20 },
		"wrong role":        func(p *pb.GraphAssemblyPlan) { p.ResolutionBatch.MediaType = ExtractionBatchMediaType },
		"unversioned":       func(p *pb.GraphAssemblyPlan) { p.RegistryView.SchemaVersion = 2 },
		"zero source":       func(p *pb.GraphAssemblyPlan) { p.DocumentBatch.ByteSize = 0 },
		"unknown root":      func(p *pb.GraphAssemblyPlan) { p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
		"unknown reference": func(p *pb.GraphAssemblyPlan) { p.RegistryView.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := proto.Clone(p).(*pb.GraphAssemblyPlan)
			mutate(copy)
			if ValidateGraphAssemblyPlan(copy) == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}
