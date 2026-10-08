// Verifies ASSEMBLE publication admission against real disposable PostgreSQL.
// Synthetic published base/child reservations exercise exact parent, registry,
// sequence and fence checks without pretending backend graph readiness or quality.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestGraphAssemblyPublicationAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("REGULAGRAPH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := Open(ctx, Config{DSN: dsn, MaxConnections: 4, HealthTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dir, _ := filepath.Abs("../../../../../migrations")
	if err = r.ApplyMigrations(ctx, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	corpus := fmt.Sprintf("corpus:graph-auth:%d", time.Now().UnixNano())
	base, err := r.ReservePublication(ctx, "base:"+corpus, "", corpus, "snapshot:base:"+corpus, "")
	if err != nil {
		t.Fatal(err)
	}
	manifest := publicationFixture(corpus, base.PublicationID, base.SnapshotID, base.Sequence, base.Fence, nil)
	if err = r.StagePublication(ctx, manifest); err != nil {
		t.Fatal(err)
	}
	if err = r.RecordBackendReceipt(ctx, receiptFixture(base.PublicationID, base.Fence)); err != nil {
		t.Fatal(err)
	}
	if err = r.CommitPublication(ctx, base.PublicationID); err != nil {
		t.Fatal(err)
	}
	target, err := r.ReservePublication(ctx, "next:"+corpus, "", corpus, "snapshot:next:"+corpus, base.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.BindPublicationRegistry(ctx, target.PublicationID, corpus, target.Fence, 1); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../../../tests/fixtures/wire-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name  string
			Value json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	plan := new(pb.GraphAssemblyPlan)
	for _, item := range fixtures.Cases {
		if item.Name == "graph-assembly-plan-valid" {
			if err = protojson.Unmarshal(item.Value, plan); err != nil {
				t.Fatal(err)
			}
		}
	}
	plan.Meta.CorpusId = corpus
	plan.Context.CorpusId = corpus
	plan.Context.SnapshotRef = proto.Clone(manifest.SnapshotRef).(*pb.SnapshotRef)
	plan.PublicationId = target.PublicationID
	plan.PublicationFence = target.Fence
	plan.TargetSequence = target.Sequence
	plan.RegistryRevision = 1
	if err = r.VerifyGraphAssemblyPublication(ctx, plan); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*pb.GraphAssemblyPlan){
		"fence":           func(p *pb.GraphAssemblyPlan) { p.PublicationFence++ },
		"revision":        func(p *pb.GraphAssemblyPlan) { p.RegistryRevision++ },
		"sequence":        func(p *pb.GraphAssemblyPlan) { p.TargetSequence++ },
		"base generation": func(p *pb.GraphAssemblyPlan) { p.Context.SnapshotRef.RepresentationGeneration = "different" },
		"base hash": func(p *pb.GraphAssemblyPlan) {
			p.Context.SnapshotRef.ManifestHash.Sha256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		"base identity": func(p *pb.GraphAssemblyPlan) { p.Context.SnapshotRef.SnapshotId = "snapshot:foreign" },
	} {
		t.Run(name, func(t *testing.T) {
			p := proto.Clone(plan).(*pb.GraphAssemblyPlan)
			mutate(p)
			if r.VerifyGraphAssemblyPublication(ctx, p) == nil {
				t.Fatal("publication drift accepted")
			}
		})
	}
	if _, err = r.pool.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence+1 WHERE corpus_id=$1`, corpus); err != nil {
		t.Fatal(err)
	}
	if r.VerifyGraphAssemblyPublication(ctx, plan) == nil {
		t.Fatal("stale live fence accepted")
	}
	if _, err = r.pool.Exec(ctx, `UPDATE corpus_state SET publisher_fence=publisher_fence-1 WHERE corpus_id=$1`, corpus); err != nil {
		t.Fatal(err)
	}
	if err = r.AbortPublication(ctx, target.PublicationID); err != nil {
		t.Fatal(err)
	}
	if r.VerifyGraphAssemblyPublication(ctx, plan) == nil {
		t.Fatal("aborted publication accepted")
	}
}
