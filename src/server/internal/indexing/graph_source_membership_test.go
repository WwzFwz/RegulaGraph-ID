// Verifies that source envelopes can cross from the actual published INDEX path
// into graph preparation only with exact receipt, inventory, scope and live pin.
// Uses disposable PostgreSQL/Qdrant plus synthetic vectors; not model quality.
package indexing

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/domain"
)

func TestPublishedGraphSourceMembershipAgainstStores(t *testing.T) {
	runInitialIndexPublication(t, false, true, "graph-membership")
}

func checkPublishedGraphMembership(t *testing.T, ctx context.Context, repo *postgres.Repository, binding domain.IndexSourceBinding) {
	t.Helper()
	pin, err := repo.PinActiveSnapshot(ctx, binding.Snapshot.CorpusId, "lease:graph-source", "reader:graph-source", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.ReleaseSnapshotPin(context.Background(), pin.LeaseID, pin.OwnerID)
	if err = repo.VerifyPublishedGraphSourceBinding(ctx, pin, binding); err != nil {
		t.Fatal("published source membership", err)
	}
	for _, name := range []string{"scope", "source job", "publication", "fence", "original ref", "bound ref", "snapshot hash", "foreign lease"} {
		t.Run(name, func(t *testing.T) {
			bad := binding
			badPin := pin
			switch name {
			case "scope":
				bad.AuthScope += "-foreign"
			case "source job":
				bad.SourceJobID += "-foreign"
			case "publication":
				bad.PublicationID += "-foreign"
			case "fence":
				bad.Fence++
			case "original ref":
				bad.Original = proto.Clone(binding.Original).(*pb.ArtifactRef)
				bad.Original.StorageKey += ".wrong"
			case "bound ref":
				bad.Bound = proto.Clone(binding.Bound).(*pb.ArtifactRef)
				bad.Bound.StorageKey += ".wrong"
			case "snapshot hash":
				bad.Snapshot = proto.Clone(binding.Snapshot).(*pb.SnapshotRef)
				bad.Snapshot.ManifestHash = &pb.ContentHash{Sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
			case "foreign lease":
				badPin.OwnerID += "-foreign"
			}
			if e := repo.VerifyPublishedGraphSourceBinding(ctx, badPin, bad); e == nil {
				t.Fatal("forged source membership admitted")
			}
		})
	}
	if err = repo.ReleaseSnapshotPin(ctx, pin.LeaseID, pin.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err = repo.VerifyPublishedGraphSourceBinding(ctx, pin, binding); err == nil {
		t.Fatal("released pin admitted")
	}
}
