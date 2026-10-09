// Exercises cross-plan graph job ownership, source ordering and publication pins.
// Fixture plan hashes are recomputed for semantic mutations so tests reach the
// intended invariant rather than merely failing byte authentication.
package domain

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func graphJobFixture(t *testing.T) GraphJobInventory {
	t.Helper()
	var inventory GraphJobInventory
	for i := 0; i < 2; i++ {
		plan, _ := graphAssemblyFixture()
		suffix := fmt.Sprintf(":%d", i)
		plan.Meta.RecordId += suffix
		plan.OutputArtifactId += suffix
		for _, ref := range []*pb.ArtifactRef{plan.DocumentBatch, plan.ExtractionBatch, plan.ResolutionBatch, plan.RegistryView} {
			ref.ArtifactId += suffix
		}
		inventory.Assignments = append(inventory.Assignments, GraphJobAssignment{JobID: "job:child" + suffix, SourceJobID: "job:source" + suffix, Plan: plan})
	}
	refreshGraphJobReferences(t, &inventory)
	return inventory
}

func refreshGraphJobReferences(t *testing.T, in *GraphJobInventory) {
	t.Helper()
	for i := range in.Assignments {
		a := &in.Assignments[i]
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(a.Plan)
		if err != nil {
			t.Fatal(err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(raw))
		a.Reference = &pb.ArtifactRef{ArtifactId: a.Plan.Meta.RecordId, SchemaVersion: 1, MediaType: GraphAssemblyPlanMediaType,
			ByteSize: uint64(len(raw)), ContentHash: &pb.ContentHash{Sha256: digest}, StorageKey: "objects/" + digest}
	}
}

func TestGraphJobInventory(t *testing.T) {
	if err := ValidateGraphJobInventory(graphJobFixture(t)); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*GraphJobInventory){
		"empty": func(in *GraphJobInventory) { in.Assignments = nil },
		"reordered sources": func(in *GraphJobInventory) {
			in.Assignments[0], in.Assignments[1] = in.Assignments[1], in.Assignments[0]
		},
		"duplicate child":         func(in *GraphJobInventory) { in.Assignments[1].JobID = in.Assignments[0].JobID },
		"duplicate source":        func(in *GraphJobInventory) { in.Assignments[1].SourceJobID = in.Assignments[0].SourceJobID },
		"child is another source": func(in *GraphJobInventory) { in.Assignments[0].JobID = in.Assignments[1].SourceJobID },
		"shared document": func(in *GraphJobInventory) {
			in.Assignments[1].Plan.DocumentBatch = proto.Clone(in.Assignments[0].Plan.DocumentBatch).(*pb.ArtifactRef)
		},
		"shared output": func(in *GraphJobInventory) {
			in.Assignments[1].Plan.OutputArtifactId = in.Assignments[0].Plan.OutputArtifactId
		},
		"revision drift": func(in *GraphJobInventory) { in.Assignments[1].Plan.RegistryRevision++ },
		"fence drift":    func(in *GraphJobInventory) { in.Assignments[1].Plan.PublicationFence++ },
		"scope drift":    func(in *GraphJobInventory) { in.Assignments[1].Plan.Context.AuthScopeRef += "-other" },
		"ontology drift": func(in *GraphJobInventory) { in.Assignments[1].Plan.OntologyHash.Sha256 = fmt.Sprintf("%064x", 2) },
		"producer drift": func(in *GraphJobInventory) { in.Assignments[1].Plan.ProducerManifest.Build += "-other" },
	} {
		t.Run(name, func(t *testing.T) {
			in := graphJobFixture(t)
			mutate(&in)
			refreshGraphJobReferences(t, &in)
			if err := ValidateGraphJobInventory(in); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
	in := graphJobFixture(t)
	in.Assignments[0].Reference.ContentHash.Sha256 = fmt.Sprintf("%064x", 0)
	if err := ValidateGraphJobInventory(in); err == nil {
		t.Fatal("corrupt plan hash accepted")
	}
}
