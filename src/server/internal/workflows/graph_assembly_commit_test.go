// Tests the verified-output commit boundary using actual Rust-admitted output
// and a faulting storage port. Lost acknowledgement may succeed only on exact
// reconciliation; cancellation detaches the bounded read, never a write retry.
// PostgreSQL atomicity and authority races have separate backend integration tests.
package workflows

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type graphCommitFixture struct {
	write func(context.Context, *pb.ProcessBatchRequest, *pb.ProcessBatchResponse, *pb.DependencyManifest) error
	read  func(context.Context, *pb.Checkpoint) (bool, error)
}

func (f graphCommitFixture) CommitGraphOutput(ctx context.Context, _ domain.SnapshotPin, _ domain.JobRecord, req *pb.ProcessBatchRequest, res *pb.ProcessBatchResponse, deps *pb.DependencyManifest) error {
	return f.write(ctx, req, res, deps)
}
func (f graphCommitFixture) GraphCheckpointCommitted(ctx context.Context, cp *pb.Checkpoint) (bool, error) {
	return f.read(ctx, cp)
}

func checkGraphCommitRecovery(t *testing.T, out *VerifiedGraphOutput) {
	t.Helper()
	for _, mode := range []string{"success", "lost acknowledgement", "not committed", "reconciliation failure", "cancel during commit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writes, reads := 0, 0
			before := out.Response()
			failure := errors.New("commit acknowledgement lost")
			port := graphCommitFixture{
				write: func(_ context.Context, req *pb.ProcessBatchRequest, res *pb.ProcessBatchResponse, deps *pb.DependencyManifest) error {
					writes++
					if !proto.Equal(req, out.request) || !proto.Equal(res, before) || !proto.Equal(deps, out.delta.Dependencies) {
						t.Fatal("commit inputs drifted")
					}
					req.JobId = "mutated"
					res.Checkpoint.Meta.RecordId = "mutated"
					deps.ArtifactId = "mutated"
					if mode == "success" {
						return nil
					}
					if mode == "cancel during commit" {
						cancel()
					}
					return failure
				},
				read: func(recovery context.Context, cp *pb.Checkpoint) (bool, error) {
					reads++
					deadline, ok := recovery.Deadline()
					if recovery.Err() != nil || !ok || time.Until(deadline) > 2*time.Second {
						t.Fatal("recovery read lacks independent bound")
					}
					if !proto.Equal(cp, before.Checkpoint) {
						t.Fatal("checkpoint mutation escaped port clone")
					}
					if mode == "reconciliation failure" {
						return false, errors.New("read unavailable")
					}
					return mode == "lost acknowledgement" || mode == "cancel during commit", nil
				},
			}
			err := out.Commit(ctx, port)
			wantSuccess := mode == "success" || mode == "lost acknowledgement" || mode == "cancel during commit"
			if (err == nil) != wantSuccess || writes != 1 {
				t.Fatal("incorrect commit result/write count", err, writes)
			}
			if mode == "success" && reads != 0 || mode != "success" && reads != 1 {
				t.Fatal("unexpected reconciliation count", reads)
			}
			if !proto.Equal(before, out.Response()) {
				t.Fatal("output mutated by storage port")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := out.Commit(ctx, graphCommitFixture{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled commit was not rejected", err)
	}
	if err := new(VerifiedGraphOutput).Commit(context.Background(), graphCommitFixture{}); err == nil {
		t.Fatal("unverified zero output accepted")
	}
}
