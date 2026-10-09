// Restores bounded authenticated source bytes for storage-owned graph admission.
// Original CHUNK/EXTRACT/RESOLVE plus bound CHUNK, canonical view and (only for
// nonempty extraction) candidate evidence come from immutable receipt locators.
// No fresh model decisions or canonical view are invented on daemon restart.
// Aggregate byte checks precede I/O; measure cold restore RSS/latency against the
// required benchmark suite separately from warm per-job dispatch.
package workflows

import (
	"context"
	"errors"
	"os"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphInventoryInputStore interface {
	LoadGraphSourceBinding(context.Context, string, string, string) (domain.GraphSourceBinding, error)
	LoadSemanticResolutionIntent(context.Context, string, string) (domain.SemanticResolutionIntent, error)
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
}

// ReadGraphInventoryInputs restores the same bounded, registered source bytes
// for daemon execution and operator publication. Returned bytes are not an
// admission capability; callers must obtain storage-owned authority afterward.
func ReadGraphInventoryInputs(ctx context.Context, store GraphInventoryInputStore, reader DocumentArtifactReader, in domain.GraphJobInventory) (map[string]domain.GraphJobSourceInputs, error) {
	if ctx == nil || store == nil || reader == nil {
		return nil, errors.New("graph input dependencies required")
	}
	if err := domain.ValidateGraphJobInventory(in); err != nil {
		return nil, err
	}
	remaining := uint64(64 << 20)
	inputs := make(map[string]domain.GraphJobSourceInputs, len(in.Assignments))
	for _, a := range in.Assignments {
		corpus := a.Plan.Meta.CorpusId
		b, err := store.LoadGraphSourceBinding(ctx, corpus, a.Plan.PublicationId, a.SourceJobID)
		if err != nil {
			return nil, graphArtifactReadError(err)
		}
		if err = domain.ValidateGraphSourceBinding(b); err != nil {
			return nil, errors.Join(domain.ErrPersistentIntegrity, err)
		}
		var input domain.GraphJobSourceInputs
		for _, role := range []struct {
			ref    *pb.ArtifactRef
			output *[]byte
		}{
			{b.Source.Original, &input.Source.OriginalDocument}, {b.Source.Bound, &input.Source.SnapshotDocument},
			{b.OriginalExtraction, &input.Source.Extraction}, {b.OriginalResolution, &input.Source.Resolution}, {a.Plan.RegistryView, &input.RegistryView},
		} {
			raw, err := readGraphPreparationArtifact(ctx, store, reader, corpus, role.ref, &remaining)
			if err != nil {
				return nil, graphArtifactReadError(err)
			}
			*role.output = raw
		}
		extraction := new(pb.ExtractionBatch)
		if err = domain.DecodeWire(input.Source.Extraction, extraction, domain.DefaultWireLimits); err != nil {
			return nil, errors.Join(domain.ErrPersistentIntegrity, err)
		}
		if len(extraction.Mentions) > 0 {
			intent, err := store.LoadSemanticResolutionIntent(ctx, corpus, a.SourceJobID)
			if err != nil {
				return nil, graphArtifactReadError(err)
			}
			if intent.CandidateRef == nil {
				return nil, domain.ErrPersistentIntegrity
			}
			input.Candidates, err = readGraphPreparationArtifact(ctx, store, reader, corpus, intent.CandidateRef, &remaining)
			if err != nil {
				return nil, graphArtifactReadError(err)
			}
		}
		inputs[a.SourceJobID] = input
	}
	return inputs, nil
}

// graphRecoveryWorker is a local response reader, never an RPC client. Feeding
// it through ExecuteGraphAssembly repeats all source/hash/projection checks.
type graphRecoveryWorker struct {
	checkpoint *pb.Checkpoint
	reference  *pb.ArtifactRef
}

func (w graphRecoveryWorker) ProcessBatch(ctx context.Context, request *pb.ProcessBatchRequest) (*pb.ProcessBatchResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, graphArtifactReadError(err)
	}
	cp := proto.Clone(w.checkpoint).(*pb.Checkpoint)
	cp.Meta.RecordId = graphAttemptID("graph-recovery-checkpoint-v1", request.JobId, cp.Meta.RecordId, request.Context.RequestId)
	cp.Fence = request.Lease.Fence
	return &pb.ProcessBatchResponse{RequestId: request.Context.RequestId, JobId: request.JobId, Attempt: request.Attempt, Fence: request.Lease.Fence,
		Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED, Checkpoint: cp, GraphDelta: proto.Clone(w.reference).(*pb.ArtifactRef)}, nil
}

func (p *GraphJobProcessor) recoveryWorker(ctx context.Context, job domain.JobRecord, plan *pb.GraphAssemblyPlan) (GraphBatchWorker, error) {
	cp, err := p.store.LoadLatestCheckpoint(ctx, job.JobID)
	if err != nil {
		return nil, graphArtifactReadError(err)
	}
	if err = domain.ValidateWire(cp, domain.DefaultWireLimits); err != nil {
		return nil, errors.Join(domain.ErrPersistentIntegrity, err)
	}
	if cp.Meta.RecordId != job.LatestCheckpointID || cp.Meta.CorpusId != job.CorpusID || cp.JobId != job.JobID || cp.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE ||
		cp.Fence >= job.LeaseFence || cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || len(cp.CompletedBatchKeys) != 1 || len(cp.ArtifactHashes) != 1 || !proto.Equal(cp.Manifest, plan.ProducerManifest) {
		return nil, domain.ErrPersistentIntegrity
	}
	ref, err := p.store.LoadArtifact(ctx, job.CorpusID, cp.CompletedBatchKeys[0])
	if err != nil {
		return nil, graphArtifactReadError(err)
	}
	if ref.MediaType != domain.GraphDeltaMediaType || !proto.Equal(ref.ContentHash, cp.ArtifactHashes[0]) {
		return nil, domain.ErrPersistentIntegrity
	}
	return graphRecoveryWorker{checkpoint: proto.Clone(cp).(*pb.Checkpoint), reference: proto.Clone(ref).(*pb.ArtifactRef)}, nil
}

// Immutable prerequisites cannot appear on an identical replay. Availability
// errors retain their original classification so transient I/O can still retry.
func graphArtifactReadError(err error) error {
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, domain.ErrPersistentIntegrity)
	}
	return err
}
