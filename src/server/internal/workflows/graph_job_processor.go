// Restores inventory-owned ASSEMBLE work, pins its published base, authenticates
// source evidence and invokes the existing worker/output-commit boundary. One
// cached admission avoids full-inventory reads for every child; each use requires
// live authorization and a failed check forces one fresh admission. Cache access
// is cancellable and bounded to one inventory (64MiB input evidence on restore).
// No registry decisions/model extraction are repeated. Measure restore/read/RPC/
// commit/queue p95 and RSS against configs/benchmark-targets.yaml, still unmeasured.
package workflows

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphExecutionAuthority interface {
	GraphDispatchAuthority
	GraphOutputCommitter
}
type GraphAdmissionFactory func(context.Context, domain.GraphJobInventory, map[string]domain.GraphJobSourceInputs) (GraphExecutionAuthority, error)
type GraphJobProcessorStore interface {
	GraphJobPublication(context.Context, domain.JobRecord) (string, error)
	LoadGraphJobInventory(context.Context, string, string) (domain.GraphJobInventory, error)
	LoadGraphSourceBinding(context.Context, string, string, string) (domain.GraphSourceBinding, error)
	LoadSemanticResolutionIntent(context.Context, string, string) (domain.SemanticResolutionIntent, error)
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	LoadLatestCheckpoint(context.Context, string) (*pb.Checkpoint, error)
	PinActiveSnapshot(context.Context, string, string, string, time.Duration) (domain.SnapshotPin, error)
	ReleaseSnapshotPin(context.Context, string, string) error
}

type GraphJobProcessor struct {
	store               GraphJobProcessorStore
	reader              DocumentArtifactReader
	worker              GraphBatchWorker
	admit               GraphAdmissionFactory
	ontology            *domain.Ontology
	scope               string
	gate                chan struct{}
	publication, corpus string
	authority           GraphExecutionAuthority
}

func NewGraphJobProcessor(store GraphJobProcessorStore, reader DocumentArtifactReader, worker GraphBatchWorker,
	admit GraphAdmissionFactory, ontology *domain.Ontology, scope string) (*GraphJobProcessor, error) {
	if store == nil || reader == nil || worker == nil || admit == nil || ontology == nil || scope == "" {
		return nil, errors.New("graph processor dependencies and authorized scope required")
	}
	return &GraphJobProcessor{store: store, reader: reader, worker: worker, admit: admit, ontology: ontology, scope: scope, gate: make(chan struct{}, 1)}, nil
}

func (p *GraphJobProcessor) ProcessGraphJob(ctx context.Context, job domain.JobRecord) (response *pb.ProcessBatchResponse, err error) {
	defer func() { err = graphArtifactReadError(err) }()
	if ctx == nil || job.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE || job.State != pb.JobState_JOB_STATE_RUNNING || job.CancellationRequested || !job.LeaseExpiresAt.After(time.Now()) {
		return nil, domain.ErrPersistentIntegrity
	}
	bounded, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	publication, err := p.store.GraphJobPublication(bounded, job)
	if err != nil {
		return nil, err
	}
	deadline, _ := bounded.Deadline()
	requestID := graphAttemptID("graph-request-v1", job.JobID, fmt.Sprint(job.Attempt), fmt.Sprint(job.LeaseFence))
	pin, err := p.store.PinActiveSnapshot(bounded, job.CorpusID, graphAttemptID("graph-read-v1", requestID), job.LeaseOwner, time.Until(deadline))
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer done()
		// Preserve a committed success: cleanup failure cannot undo STAGED. The
		// bounded read lease expires automatically; report failure on failed work.
		releaseErr := p.store.ReleaseSnapshotPin(cleanup, pin.LeaseID, pin.OwnerID)
		if releaseErr != nil {
			slog.Warn("release ASSEMBLE snapshot pin", "job_id", job.JobID, "error", releaseErr)
		}
		if err != nil {
			err = errors.Join(err, releaseErr)
		}
	}()
	authority, err := p.admitted(bounded, publication, pin, job)
	if err != nil {
		return nil, err
	}
	a, err := authority.AuthorizeGraphDispatch(bounded, pin, job)
	if err != nil {
		return nil, err
	}
	if a.Plan.Context.AuthScopeRef != p.scope || !proto.Equal(a.Plan.OntologyHash, p.ontology.ContentHash()) {
		return nil, domain.ErrPersistentIntegrity
	}
	call := proto.Clone(a.Plan.Context).(*pb.RequestContext)
	call.RequestId, call.TraceId, call.Deadline = requestID, requestID, timestamppb.New(deadline)
	worker := p.worker
	if job.LatestCheckpointID != "" {
		worker, err = p.recoveryWorker(bounded, job, a.Plan)
		if err != nil {
			return nil, err
		}
	}
	out, err := ExecuteGraphAssembly(bounded, authority, p.reader, worker, pin, job, call, p.ontology)
	if err != nil {
		return nil, err
	}
	if err = out.Commit(bounded, authority); err != nil {
		return nil, err
	}
	return out.Response(), nil
}

func (p *GraphJobProcessor) admitted(ctx context.Context, publication string, pin domain.SnapshotPin, job domain.JobRecord) (GraphExecutionAuthority, error) {
	select {
	case p.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.gate }()
	if p.authority != nil && p.publication == publication && p.corpus == job.CorpusID {
		if _, err := p.authority.AuthorizeGraphDispatch(ctx, pin, job); err == nil {
			return p.authority, nil
		}
	}
	in, err := p.store.LoadGraphJobInventory(ctx, job.CorpusID, publication)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateGraphJobInventory(in); err != nil {
		return nil, errors.Join(domain.ErrPersistentIntegrity, err)
	}
	first := in.Assignments[0].Plan
	if first.Meta.CorpusId != job.CorpusID || first.PublicationId != publication || first.Context.AuthScopeRef != p.scope || !proto.Equal(first.OntologyHash, p.ontology.ContentHash()) {
		return nil, domain.ErrPersistentIntegrity
	}
	inputs, err := p.readInventoryInputs(ctx, in)
	if err != nil {
		return nil, err
	}
	authority, err := p.admit(ctx, in, inputs)
	if err != nil {
		return nil, err
	}
	if authority == nil {
		return nil, errors.New("graph factory returned no authority")
	}
	if _, err = authority.AuthorizeGraphDispatch(ctx, pin, job); err != nil {
		return nil, err
	}
	p.publication, p.corpus, p.authority = publication, job.CorpusID, authority
	return authority, nil
}

func graphAttemptID(kind string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, v := range parts {
		h.Write([]byte{0})
		h.Write([]byte(v))
	}
	return kind + ":" + fmt.Sprintf("%x", h.Sum(nil))
}
