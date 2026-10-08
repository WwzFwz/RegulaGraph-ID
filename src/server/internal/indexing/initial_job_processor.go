// Connects a claimed durable INDEX job to admitted plans, the Rust worker,
// immutable registration and atomic checkpoint/STAGED commit. A single cached
// inventory avoids re-reading the complete corpus for every child; per-job live
// authority and per-batch bytes are still rechecked. Workflow owns claiming,
// cancellation polling and retry. Cache memory is bounded by initial-plan limits;
// measure restore/cache/RPC/commit p95 and RSS under benchmark-targets.yaml.
package indexing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type IndexJobProcessorStore interface {
	IndexAuthority
	IndexPlanRegistry
	IndexCheckpointStore
	IndexJobPublication(context.Context, domain.JobRecord) (string, error)
	LoadIndexJobInventory(context.Context, string) (domain.IndexJobInventory, error)
	IndexCheckpointCommitted(context.Context, *pb.Checkpoint) (bool, error)
}

type InitialIndexJobProcessor struct {
	store       IndexJobProcessorStore
	reader      IndexArtifactReader
	worker      IndexBatchWorker
	scope       string
	mu          sync.Mutex
	publication string
	plans       *InitialIndexPlans
}

func NewInitialIndexJobProcessor(store IndexJobProcessorStore, reader IndexArtifactReader, worker IndexBatchWorker, scope string) (*InitialIndexJobProcessor, error) {
	if store == nil || reader == nil || worker == nil || scope == "" {
		return nil, errors.New("INDEX store, reader, worker and authorized scope required")
	}
	return &InitialIndexJobProcessor{store: store, reader: reader, worker: worker, scope: scope}, nil
}

func (p *InitialIndexJobProcessor) admitted(ctx context.Context, publication string) (*InitialIndexPlans, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.plans != nil && p.publication == publication {
		return p.plans, nil
	}
	in, err := p.store.LoadIndexJobInventory(ctx, publication)
	if err != nil {
		return nil, err
	}
	if in.AuthScope != p.scope {
		return nil, fmt.Errorf("INDEX inventory scope: %w", domain.ErrPersistentIntegrity)
	}
	plans, err := RestoreInitialIndexPlans(ctx, p.store, p.reader, in)
	if err != nil {
		return nil, err
	}
	p.publication, p.plans = publication, plans
	return plans, nil
}

func (p *InitialIndexJobProcessor) ProcessIndexJob(ctx context.Context, job domain.JobRecord) (*pb.ProcessBatchResponse, error) {
	publication, err := p.store.IndexJobPublication(ctx, job)
	if err != nil {
		return nil, err
	}
	plans, err := p.admitted(ctx, publication)
	if err != nil {
		return nil, err
	}
	position := -1
	for i, batch := range plans.plans {
		if initialPlanID("index-job-v1", publication, batch.Reference.ArtifactId) == job.JobID {
			position = i
			break
		}
	}
	if position < 0 {
		return nil, fmt.Errorf("INDEX job absent from admitted inventory: %w", domain.ErrPersistentIntegrity)
	}
	batch := plans.plans[position]
	deadline := job.LeaseExpiresAt
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	requestID := initialPlanID("index-request-v1", job.JobID, fmt.Sprint(job.Attempt), fmt.Sprint(job.LeaseFence))
	call := &pb.RequestContext{SchemaVersion: 1, RequestId: requestID, TraceId: requestID, CorpusId: job.CorpusID,
		SnapshotRef: proto.Clone(batch.Plan.TargetSnapshot).(*pb.SnapshotRef), Deadline: timestamppb.New(deadline),
		ConfigFingerprint: proto.Clone(batch.Plan.Producer.ConfigHash).(*pb.ContentHash), AuthScopeRef: p.scope}
	request, err := plans.WorkerRequest(position, job, call)
	if err != nil {
		return nil, err
	}
	output, err := plans.ExecuteBatch(ctx, position, request, p.worker, p.store, p.reader)
	if err != nil {
		return nil, err
	}
	if err = output.Register(ctx, p.store); err != nil {
		return nil, err
	}
	if err = output.Commit(ctx, job.LeaseOwner, p.store); err != nil {
		// Commit may have succeeded before its response was lost. Only an exact
		// durable checkpoint/STAGED match permits reporting that success.
		confirm, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		committed, checkErr := p.store.IndexCheckpointCommitted(confirm, output.Response().Checkpoint)
		if !committed || checkErr != nil {
			return nil, errors.Join(err, checkErr)
		}
	}
	return output.Response(), nil
}
