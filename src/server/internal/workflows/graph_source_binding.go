// Binds authenticated CHUNK/EXTRACT/RESOLVE artifacts to a published base snapshot
// and persists deterministic derived artifacts plus a fenced storage receipt.
// A retry repeats immutable writes and receipt admission without sampling models.
// Interrupted object writes may leave orphans, never a partially committed receipt
// or scheduled ASSEMBLE job. Cross-revision policy includes historical candidate
// bytes for storage-side reaffirmation; assembly repeats freshness/receipt checks.
// Four envelope inputs are capped at 16MiB, candidate input separately at 16MiB;
// measure read/hash/put/commit p95 and orphan/retry rate under benchmark-targets.yaml.
package workflows

import (
	"bytes"
	"context"
	"errors"
	"io"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphSourceBindingStore interface {
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	RegisterArtifact(context.Context, string, *pb.ArtifactRef) error
	EnsureArtifactDependencyManifest(context.Context, string, string, *pb.DependencyManifest) error
	VerifyPublishedGraphSourceBinding(context.Context, domain.SnapshotPin, domain.IndexSourceBinding) error
	VerifyGraphAssemblySourceCheckpoint(context.Context, string, string, string, *pb.ArtifactRef) error
	RegisterGraphSourceBinding(context.Context, domain.SnapshotPin, domain.GraphSourceBinding, domain.GraphSourceBindingInputs, int) error
	LoadSemanticResolutionIntent(context.Context, string, string) (domain.SemanticResolutionIntent, error)
}

type GraphSourceArtifactWriter interface {
	Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error)
}

func BindGraphSource(ctx context.Context, store GraphSourceBindingStore, reader DocumentArtifactReader,
	writer GraphSourceArtifactWriter, pin domain.SnapshotPin, request domain.GraphSourceBinding, maximumEdges int) (domain.GraphSourceBinding, error) {
	var empty domain.GraphSourceBinding
	if ctx == nil || store == nil || reader == nil || writer == nil || maximumEdges <= 0 || maximumEdges > domain.DefaultWireLimits.MaxItems {
		return empty, errors.New("bounded graph binding dependencies required")
	}
	if err := domain.ValidateGraphSourceBindingRequest(request); err != nil {
		return empty, err
	}
	bounded, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return empty, err
	}
	if err := store.VerifyPublishedGraphSourceBinding(bounded, pin, request.Source); err != nil {
		return empty, err
	}
	corpus := request.Source.Snapshot.CorpusId
	if err := store.VerifyGraphAssemblySourceCheckpoint(bounded, corpus, request.Source.SourceJobID, request.SourceCheckpointID, request.OriginalResolution); err != nil {
		return empty, err
	}
	refs := []*pb.ArtifactRef{request.Source.Original, request.Source.Bound, request.OriginalExtraction, request.OriginalResolution}
	remaining := uint64(domain.DefaultWireLimits.MaxBytes)
	for _, ref := range refs {
		if ref.ByteSize > remaining {
			return empty, errors.New("graph binding input exceeds byte budget")
		}
		remaining -= ref.ByteSize
	}
	inputs := make([]domain.GraphSourceArtifact, 0, len(refs))
	for _, ref := range refs {
		registered, err := store.LoadArtifact(bounded, corpus, ref.ArtifactId)
		if err != nil {
			return empty, err
		}
		if !proto.Equal(registered, ref) {
			return empty, domain.ErrPersistentIntegrity
		}
		raw, err := reader.ReadVerified(bounded, ref, ref.ByteSize)
		if err != nil {
			return empty, err
		}
		inputs = append(inputs, domain.GraphSourceArtifact{Reference: ref, Bytes: raw})
	}
	input := domain.GraphSourceBindingInputs{OriginalDocument: inputs[0].Bytes, SnapshotDocument: inputs[1].Bytes, Extraction: inputs[2].Bytes, Resolution: inputs[3].Bytes}
	if request.Policy == domain.GraphSourceReaffirmationPolicy {
		e := new(pb.ExtractionBatch)
		if err := domain.DecodeWire(input.Extraction, e, domain.DefaultWireLimits); err != nil {
			return empty, err
		}
		if len(e.Mentions) > 0 {
			intent, err := store.LoadSemanticResolutionIntent(bounded, corpus, request.Source.SourceJobID)
			if err != nil {
				return empty, err
			}
			// Candidates are a separately bounded historical input; the whole
			// inventory admission still enforces the aggregate 64MiB limit.
			candidateBudget := uint64(domain.DefaultWireLimits.MaxBytes)
			input.Candidates, err = readGraphPreparationArtifact(bounded, store, reader, corpus, intent.CandidateRef, &candidateBudget)
			if err != nil {
				return empty, err
			}
		}
	}
	extract, resolve, err := domain.BindGraphSourceReceiptEnvelopes(request, input, maximumEdges)
	if err != nil {
		return empty, err
	}
	request.BoundExtraction, request.BoundResolution = extract.Reference, resolve.Reference
	if err = domain.ValidateGraphSourceBinding(request); err != nil {
		return empty, err
	}
	for i, artifact := range []domain.GraphSourceArtifact{extract, resolve} {
		if err = bounded.Err(); err != nil {
			return empty, err
		}
		if _, err = writer.Put(bounded, artifact.Reference, bytes.NewReader(artifact.Bytes)); err != nil {
			return empty, err
		}
		if err = store.RegisterArtifact(bounded, corpus, artifact.Reference); err != nil {
			return empty, err
		}
		var dependencies *pb.DependencyManifest
		if i == 0 {
			m := new(pb.ExtractionBatch)
			if err = domain.DecodeWire(artifact.Bytes, m, domain.DefaultWireLimits); err != nil {
				return empty, err
			}
			dependencies = m.Dependencies
		} else {
			m := new(pb.ResolutionBatch)
			if err = domain.DecodeWire(artifact.Bytes, m, domain.DefaultWireLimits); err != nil {
				return empty, err
			}
			dependencies = m.Dependencies
		}
		// Wire manifests identify the logical batch; the dependency index owns the
		// content-addressed artifact. Retain the serialized manifest unchanged.
		dependencies = proto.Clone(dependencies).(*pb.DependencyManifest)
		dependencies.ArtifactId = artifact.Reference.ArtifactId
		if err = store.EnsureArtifactDependencyManifest(bounded, corpus, artifact.Reference.ArtifactId, dependencies); err != nil {
			return empty, err
		}
	}
	if err = store.RegisterGraphSourceBinding(bounded, pin, request, input, maximumEdges); err != nil {
		return empty, err
	}
	return request, nil
}
