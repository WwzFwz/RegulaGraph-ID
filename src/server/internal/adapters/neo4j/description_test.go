// Proves offline generation description rejects cross-delta immutable conflicts
// before any schema or network access. The same projection drives exact sealing;
// a description alone is deliberately not a backend-ready receipt.
package neo4j

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestDescribeGraphWithoutNetwork(t *testing.T) {
	b, d := graphFixture("corpus:offline-description")
	s, err := New(Config{URI: "bolt://127.0.0.1:1", Username: "test", Password: "test", Database: "neo4j", PoolSize: 1, Timeout: time.Second}, b)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	second := proto.Clone(d).(*pb.GraphDelta)
	second.Meta.RecordId += "-second"
	second.Supports[0].Meta.RecordId += "-second"
	proof, err := s.Describe([]*pb.GraphDelta{d, second})
	if err != nil || proof.Records != 5 || proof.Edges != 4 || proof.Operations != 2 || s.schemaReady.Load() {
		t.Fatal("offline shared-support description", proof, err)
	}
	reordered, err := s.Describe([]*pb.GraphDelta{second, d})
	if err != nil || reordered != proof {
		t.Fatal("description depends on delta order", err)
	}
	second.Entities[0].PreferredLabel += " changed"
	if _, err = s.Describe([]*pb.GraphDelta{d, second}); err == nil {
		t.Fatal("conflicting shared entity accepted before write")
	}
	if _, err = s.Describe([]*pb.GraphDelta{d, d}); err == nil {
		t.Fatal("duplicate delta accepted")
	}
	if _, err = s.Describe(nil); err == nil {
		t.Fatal("missing inventory accepted")
	}
	copy := s.Binding()
	copy.BaseSnapshot.SnapshotId = "snapshot:changed"
	if s.Binding().BaseSnapshot.SnapshotId == copy.BaseSnapshot.SnapshotId {
		t.Fatal("binding aliases caller memory")
	}
}
