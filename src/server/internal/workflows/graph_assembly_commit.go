// Commits only outputs admitted by ExecuteGraphAssembly. The storage port owns
// one transaction for metadata, dependencies, checkpoint and STAGED, repeating
// live source/registry/lease checks. Lost acknowledgements are reconciled through
// exact stored checkpoint bytes with a bounded independent read. No model/RPC is
// rerun and STAGED does not imply graph publication. Measure commit/recovery p95
// and cancellation lag under configs/benchmark-targets.yaml; acceptance is pending.
package workflows

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphOutputCommitter interface {
	CommitGraphOutput(context.Context, domain.SnapshotPin, domain.JobRecord, *pb.ProcessBatchRequest, *pb.ProcessBatchResponse, *pb.DependencyManifest) error
	GraphCheckpointCommitted(context.Context, *pb.Checkpoint) (bool, error)
}

func (o *VerifiedGraphOutput) Commit(ctx context.Context, storage GraphOutputCommitter) error {
	if ctx == nil || storage == nil || o == nil || o.request == nil || o.response == nil || o.delta == nil {
		return errors.New("verified graph output and commit storage required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := storage.CommitGraphOutput(ctx, o.pin, o.job, proto.Clone(o.request).(*pb.ProcessBatchRequest),
		proto.Clone(o.response).(*pb.ProcessBatchResponse), proto.Clone(o.delta.Dependencies).(*pb.DependencyManifest))
	if err == nil {
		return nil
	}
	// A cancelled/failed acknowledgement does not establish that COMMIT failed.
	// This read cannot write or authorize a new attempt after cancellation.
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	committed, checkErr := storage.GraphCheckpointCommitted(recovery, proto.Clone(o.response.Checkpoint).(*pb.Checkpoint))
	if checkErr == nil && committed {
		return nil
	}
	return errors.Join(err, checkErr)
}
