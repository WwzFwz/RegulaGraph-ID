// Defines the local durable receipt for a graph source metadata transform.
// Existing C01 artifact/snapshot references remain the wire authority. This Go
// storage record binds original and derived EXTRACT/RESOLVE refs to one source
// checkpoint, published CHUNK membership and reserved target. The reaffirmation
// policy records checked reuse at a later registry view; it does not prove model
// correctness or grant permanent authority. ASSEMBLE repeats live admission.
// Receipt size is capped at 64KiB by storage; measure admission/hash/lock p95 and
// replay consistency under benchmark-targets.yaml (REQUIRED_UNMEASURED).
package domain

import (
	"errors"
	"math"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type GraphSourceBinding struct {
	Policy                                  string
	PublicationID                           string
	Fence, TargetSequence, RegistryRevision uint64
	SourceCheckpointID                      string
	Source                                  IndexSourceBinding
	OriginalExtraction, OriginalResolution  *pb.ArtifactRef
	BoundExtraction, BoundResolution        *pb.ArtifactRef
}

func ValidateGraphSourceBinding(binding GraphSourceBinding) error {
	return validateGraphSourceBinding(binding, false)
}

// ValidateGraphSourceBindingRequest admits pre-transform inputs only. The workflow
// fills both derived refs; caller-supplied output refs are not accepted as a plan.
func ValidateGraphSourceBindingRequest(binding GraphSourceBinding) error {
	if binding.BoundExtraction != nil || binding.BoundResolution != nil {
		return errors.New("graph source binding request already has outputs")
	}
	return validateGraphSourceBinding(binding, true)
}

func validateGraphSourceBinding(binding GraphSourceBinding, request bool) error {
	if err := ValidateIndexSourceBinding(binding.Source); err != nil {
		return err
	}
	if (binding.Policy != GraphSourceEnvelopePolicy && binding.Policy != GraphSourceReaffirmationPolicy) || binding.PublicationID == binding.Source.PublicationID ||
		binding.Fence == 0 || binding.Fence > math.MaxInt64 || binding.TargetSequence <= binding.Source.Snapshot.Sequence ||
		binding.TargetSequence > math.MaxInt64 || binding.RegistryRevision == 0 || binding.RegistryRevision > math.MaxInt64 {
		return errors.New("invalid graph source publication binding")
	}
	for _, id := range []string{binding.PublicationID, binding.SourceCheckpointID} {
		if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: binding.Source.Snapshot.CorpusId, RecordId: id}, DefaultWireLimits); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	refs := []*pb.ArtifactRef{binding.Source.Original, binding.Source.Bound, binding.OriginalExtraction, binding.OriginalResolution}
	if !request {
		refs = append(refs, binding.BoundExtraction, binding.BoundResolution)
	}
	for _, ref := range refs {
		if err := ValidateWire(ref, DefaultWireLimits); err != nil {
			return err
		}
		if !graphAssemblyKnownFields(ref.ProtoReflect()) || ref.SchemaVersion != 1 || ref.ByteSize == 0 || ref.ByteSize > uint64(DefaultWireLimits.MaxBytes) || seen[ref.ArtifactId] {
			return errors.New("graph binding has invalid or overlapping artifact roles")
		}
		seen[ref.ArtifactId] = true
	}
	if !graphAssemblyKnownFields(binding.Source.Snapshot.ProtoReflect()) ||
		(binding.OriginalExtraction.MediaType != ExtractionBatchMediaType && binding.OriginalExtraction.MediaType != "application/x-protobuf") ||
		(binding.OriginalResolution.MediaType != "application/x-protobuf" && binding.OriginalResolution.MediaType != "application/x-protobuf; message=regulagraph.v1.ResolutionBatch") {
		return errors.New("graph binding media, source identity or snapshot fields mismatch")
	}
	if !request && (binding.BoundExtraction.MediaType != binding.OriginalExtraction.MediaType || binding.BoundResolution.MediaType != binding.OriginalResolution.MediaType ||
		proto.Equal(binding.OriginalExtraction.ContentHash, binding.BoundExtraction.ContentHash) || proto.Equal(binding.OriginalResolution.ContentHash, binding.BoundResolution.ContentHash)) {
		return errors.New("graph binding derived artifact identity mismatch")
	}
	return nil
}

// GraphSourceBindingInputs contains original CHUNK, bound CHUNK, original EXTRACT
// and original RESOLVE bytes, in named roles. Derived bytes are recomputed exactly.
type GraphSourceBindingInputs struct {
	OriginalDocument, SnapshotDocument, Extraction, Resolution []byte
	// Candidates is used only for authenticated cross-revision receipt creation;
	// it is not serialized into the source-binding receipt or sent to Rust.
	Candidates []byte
}
