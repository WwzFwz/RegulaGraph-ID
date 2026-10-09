// Checks that graph reuse never drops unhandled EXTRACT dependencies. Fixtures
// retain production wire shapes; these checks do not replace the PostgreSQL
// candidate-view and immutable decision-ledger integration tests.
package workflows

import (
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestGraphExtractionDependencies(t *testing.T) {
	artifacts := graphEnvelopeFixture(t, false)
	extraction := new(pb.ExtractionBatch)
	if err := domain.DecodeWire(artifacts[2].Bytes, extraction, domain.DefaultWireLimits); err != nil {
		t.Fatal(err)
	}
	if err := validateGraphExtractionDependencies(extraction); err != nil {
		t.Fatal("production dependency shape rejected", err)
	}
	for name, mutate := range map[string]func(*pb.ExtractionBatch){
		"missing source": func(b *pb.ExtractionBatch) { b.Dependencies.Dependencies = nil },
		"duplicate source": func(b *pb.ExtractionBatch) {
			b.Dependencies.Dependencies = append(b.Dependencies.Dependencies, proto.Clone(b.Dependencies.Dependencies[0]).(*pb.Dependency))
		},
		"wrong source hash": func(b *pb.ExtractionBatch) { b.Dependencies.Dependencies[0].Fingerprint = parseHash("9") },
		"external artifact": func(b *pb.ExtractionBatch) {
			b.Dependencies.Dependencies = append(b.Dependencies.Dependencies, &pb.Dependency{DependencyId: "artifact:unverified", Fingerprint: parseHash("9")})
		},
		"negative lookup": func(b *pb.ExtractionBatch) {
			b.Dependencies.LookupScopeRevisions = []*pb.LookupScopeRevision{{ScopeId: "scope:unknown", EmptyResult: true}}
		},
		"positive lookup": func(b *pb.ExtractionBatch) {
			b.Dependencies.LookupScopeRevisions = []*pb.LookupScopeRevision{{ScopeId: "scope:unknown", Revision: 1}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := proto.Clone(extraction).(*pb.ExtractionBatch)
			mutate(changed)
			if err := validateGraphExtractionDependencies(changed); err == nil {
				t.Fatal("unchecked extraction dependency accepted")
			}
		})
	}
}
