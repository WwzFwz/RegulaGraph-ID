// Exercises reconstruction against real BIND materialization and altered source
// observations/identity paths. These tests prove exact key derivation and coverage,
// not historical database visibility, which is tested by the PostgreSQL adapter.
package domain

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func registryBoundDocumentFixture(t *testing.T) *pb.DocumentBatch {
	t.Helper()
	input, binding := bindingFixture()
	candidates, err := PlanProvisionIdentities(input, []RegulationDocumentBinding{binding}, 100)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := BindDocumentBatch(input, []RegulationDocumentBinding{binding}, candidates, provisionAssignments(t, candidates, 8), bindingConfig(8))
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestDocumentRegistryDependenciesReconstructBindClaims(t *testing.T) {
	batch := registryBoundDocumentFixture(t)
	before := proto.Clone(batch)
	first, err := PlanDocumentRegistryDependencies(batch, 100)
	if err != nil {
		t.Fatal(err)
	}
	if first.ObservedRevision != 8 || len(first.Identities) != 4 {
		t.Fatalf("incomplete binding plan: %+v", first)
	}
	second, err := PlanDocumentRegistryDependencies(batch, 100)
	if err != nil || !reflect.DeepEqual(first, second) || !proto.Equal(batch, before) {
		t.Fatal("non-deterministic/mutating identity reconstruction", err)
	}
	_, binding := bindingFixture()
	issuer, _ := binding.Candidate.CanonicalIssuerClaim()
	regulation, _ := binding.Candidate.canonicalRegulationClaim(binding.IssuerID)
	byID := map[string]DocumentRegistryIdentity{}
	for _, identity := range first.Identities {
		byID[identity.CanonicalID] = identity
	}
	if byID[binding.IssuerID].IdentityKey != issuer.IdentityKey || byID[binding.RegulationID].IdentityKey != regulation.IdentityKey {
		t.Fatal("reconstruction differs from original BIND claims")
	}
}

func TestDocumentRegistryDependenciesRejectIncompleteOrChangedSource(t *testing.T) {
	for name, mutate := range map[string]func(*pb.DocumentBatch){
		"missing observation":       func(b *pb.DocumentBatch) { b.Observations = nil },
		"changed number":            func(b *pb.DocumentBatch) { setObservationText(b.Observations[0], "number", "999") },
		"missing issuer dependency": func(b *pb.DocumentBatch) { b.DependencyManifest.Dependencies = b.DependencyManifest.Dependencies[:1] },
		"unknown scope":             func(b *pb.DocumentBatch) { b.DependencyManifest.LookupScopeRevisions[0].ScopeId = "alias:unhandled" },
		"empty lookup":              func(b *pb.DocumentBatch) { b.DependencyManifest.LookupScopeRevisions[0].EmptyResult = true },
		"missing revision":          func(b *pb.DocumentBatch) { b.DependencyManifest.LookupScopeRevisions = nil },
		"duplicate dependency": func(b *pb.DocumentBatch) {
			b.DependencyManifest.Dependencies = append(b.DependencyManifest.Dependencies, proto.Clone(b.DependencyManifest.Dependencies[0]).(*pb.Dependency))
		},
		"duplicate provision": func(b *pb.DocumentBatch) {
			b.Provisions = append(b.Provisions, proto.Clone(b.Provisions[0]).(*pb.Provision))
		},
		"foreign observation corpus":  func(b *pb.DocumentBatch) { b.Observations[0].Meta.CorpusId = "corpus:foreign" },
		"foreign source corpus":       func(b *pb.DocumentBatch) { b.Sources[0].Meta.CorpusId = "corpus:foreign" },
		"foreign edition corpus":      func(b *pb.DocumentBatch) { b.Editions[0].Meta.CorpusId = "corpus:foreign" },
		"edition without observation": func(b *pb.DocumentBatch) { b.Editions[0].SourceRefs = []string{"observation:missing"} },
		"unsourced regulation": func(b *pb.DocumentBatch) {
			extra := proto.Clone(b.Regulations[0]).(*pb.Regulation)
			extra.Meta.RecordId += "-extra"
			b.Regulations = append(b.Regulations, extra)
		},
		"foreign provision": func(b *pb.DocumentBatch) { b.Provisions[0].RegulationId = "regulation:foreign" },
		"partial":           func(b *pb.DocumentBatch) { b.Completeness = pb.Completeness_COMPLETENESS_PARTIAL },
	} {
		t.Run(name, func(t *testing.T) {
			b := registryBoundDocumentFixture(t)
			mutate(b)
			if _, err := PlanDocumentRegistryDependencies(b, 100); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
}

func TestDocumentRegistryDependenciesEnforceWorkBudget(t *testing.T) {
	batch := registryBoundDocumentFixture(t)
	// Records fit this limit, but their complete reference closure does not.
	limit := len(batch.Sources) + len(batch.Observations) + len(batch.Editions) + len(batch.Regulations) + len(batch.Provisions)
	if _, err := PlanDocumentRegistryDependencies(batch, limit); err == nil {
		t.Fatal("reference work escaped document budget")
	}
}
