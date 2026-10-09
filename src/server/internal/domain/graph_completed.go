// Describes the complete committed ASSEMBLE inventory handed to graph publication.
// These local values reuse C01 plans/checkpoints/artifact refs; they are not a new
// wire schema or proof of storage authority. Validation preserves ordered ownership,
// exact producer, checkpoint coverage and physical-versus-logical output identity.
// Bound aggregate metadata/output bytes before reads; measure collection/admission
// time and RSS under configs/benchmark-targets.yaml (required gates unmeasured).
package domain

import (
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type CompletedGraphInventory struct {
	Inventory   GraphJobInventory
	Checkpoints []*pb.Checkpoint
	Outputs     []*pb.ArtifactRef
}

func ValidateCompletedGraphInventory(in CompletedGraphInventory) error {
	if err := ValidateGraphJobInventory(in.Inventory); err != nil {
		return err
	}
	if len(in.Outputs) != len(in.Inventory.Assignments) || len(in.Checkpoints) != len(in.Outputs) {
		return errors.New("complete graph output/checkpoint coverage required")
	}
	seen, checkpoints, inputs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range in.Inventory.Assignments {
		for _, ref := range []*pb.ArtifactRef{a.Reference, a.Plan.DocumentBatch, a.Plan.ExtractionBatch, a.Plan.ResolutionBatch, a.Plan.RegistryView} {
			inputs[ref.ArtifactId] = true
		}
	}
	metadataRemaining, outputRemaining := 64<<20, uint64(64<<20)
	for i, a := range in.Inventory.Assignments {
		cp, ref := in.Checkpoints[i], in.Outputs[i]
		for _, value := range []proto.Message{cp, ref} {
			if err := ValidateWire(value, DefaultWireLimits); err != nil {
				return err
			}
			if !graphAssemblyKnownFields(value.ProtoReflect()) {
				return errors.New("unknown completed graph metadata fields")
			}
			if size := proto.Size(value); size > metadataRemaining {
				return errors.New("graph result metadata exceeds inventory budget")
			} else {
				metadataRemaining -= size
			}
		}
		if cp.Meta.SchemaVersion != 1 || cp.Meta.Visibility != nil || cp.Meta.CorpusId != a.Plan.Meta.CorpusId || cp.JobId != a.JobID ||
			cp.Stage != pb.JobStage_JOB_STAGE_ASSEMBLE || cp.TerminalStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED ||
			len(cp.CompletedBatchKeys) != 1 || len(cp.ArtifactHashes) != 1 || !proto.Equal(cp.Manifest, a.Plan.ProducerManifest) ||
			cp.CompletedBatchKeys[0] != ref.ArtifactId || !proto.Equal(cp.ArtifactHashes[0], ref.ContentHash) ||
			ref.SchemaVersion != 1 || ref.MediaType != GraphDeltaMediaType || ref.ByteSize == 0 || ref.ByteSize > uint64(DefaultWireLimits.MaxBytes) ||
			seen[ref.ArtifactId] || inputs[ref.ArtifactId] || checkpoints[cp.Meta.RecordId] {
			return errors.New("graph result differs from assignment or has duplicate ownership")
		}
		if ref.ByteSize > outputRemaining {
			return errors.New("graph output inventory exceeds byte budget")
		}
		outputRemaining -= ref.ByteSize
		seen[ref.ArtifactId], checkpoints[cp.Meta.RecordId] = true, true
	}
	return nil
}
