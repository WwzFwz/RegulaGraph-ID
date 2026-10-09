// Defines a frozen ASSEMBLE inventory using existing C01 plans and artifact refs.
// One source contributes one complete graph plan; all jobs share publication,
// base snapshot, registry view revision, ontology, producer and authorization scope.
// This validates shape/hash/ownership only: PostgreSQL must authenticate sources
// and freshness before enqueue. Bound inventory bytes and measure scheduling RSS,
// lock/queue p95/p99 under configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type GraphJobAssignment struct {
	JobID, SourceJobID string
	Plan               *pb.GraphAssemblyPlan
	Reference          *pb.ArtifactRef
}

type GraphJobInventory struct {
	Assignments []GraphJobAssignment
}

// GraphJobSourceInputs carries immutable evidence for one inventory assignment.
// Keys match source jobs; storage authenticates every byte before optimistic
// admission and freezes the registry revision checked under the eventual lock.
type GraphJobSourceInputs struct {
	Source                   GraphSourceBindingInputs
	Candidates, RegistryView []byte
}

// ValidateGraphJobInventory enforces exact plan bytes and whole-source ownership.
// Plans must be sorted by source job so callers cannot reorder an immutable replay.
func ValidateGraphJobInventory(in GraphJobInventory) error {
	if len(in.Assignments) == 0 || len(in.Assignments) > 256 {
		return errors.New("bounded nonempty ASSEMBLE inventory required")
	}
	jobs, sources, artifacts := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var base *pb.GraphAssemblyPlan
	remaining := 64 << 20
	for i, assignment := range in.Assignments {
		plan, ref := assignment.Plan, assignment.Reference
		if err := ValidateGraphAssemblyPlan(plan); err != nil {
			return err
		}
		if err := ValidateWire(ref, DefaultWireLimits); err != nil {
			return err
		}
		for _, id := range []string{assignment.JobID, assignment.SourceJobID} {
			if !validDocumentID(id) {
				return errors.New("valid ASSEMBLE job/source IDs required")
			}
		}
		if assignment.JobID == assignment.SourceJobID || jobs[assignment.JobID] || sources[assignment.SourceJobID] ||
			i > 0 && in.Assignments[i-1].SourceJobID >= assignment.SourceJobID {
			return errors.New("ASSEMBLE inventory has duplicate or unordered job/source ownership")
		}
		jobs[assignment.JobID], sources[assignment.SourceJobID] = true, true
		if ref.ArtifactId != plan.Meta.RecordId || ref.MediaType != GraphAssemblyPlanMediaType || ref.SchemaVersion != 1 ||
			!graphAssemblyKnownFields(ref.ProtoReflect()) {
			return errors.New("ASSEMBLE inventory reference differs from plan")
		}
		if base == nil {
			base = plan
		}
		if plan.Meta.CorpusId != base.Meta.CorpusId || plan.PublicationId != base.PublicationId || plan.PublicationFence != base.PublicationFence ||
			plan.TargetSequence != base.TargetSequence || plan.RegistryRevision != base.RegistryRevision ||
			plan.Context.AuthScopeRef != base.Context.AuthScopeRef || !proto.Equal(plan.Context.SnapshotRef, base.Context.SnapshotRef) ||
			!proto.Equal(plan.Context.ConfigFingerprint, base.Context.ConfigFingerprint) || !proto.Equal(plan.OntologyHash, base.OntologyHash) ||
			!proto.Equal(plan.ProducerManifest, base.ProducerManifest) {
			return errors.New("ASSEMBLE inventory mixes publication, source context or producer")
		}
		for _, id := range []string{plan.Meta.RecordId, plan.OutputArtifactId, plan.DocumentBatch.ArtifactId,
			plan.ExtractionBatch.ArtifactId, plan.ResolutionBatch.ArtifactId, plan.RegistryView.ArtifactId} {
			if artifacts[id] {
				return errors.New("ASSEMBLE inventory shares an owned source or output artifact")
			}
			artifacts[id] = true
		}
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(plan)
		if err != nil {
			return err
		}
		if len(raw) > remaining || uint64(len(raw)) != ref.ByteSize || fmt.Sprintf("%x", sha256.Sum256(raw)) != ref.ContentHash.Sha256 {
			return errors.New("ASSEMBLE inventory plan bytes mismatch or exceed budget")
		}
		remaining -= len(raw)
	}
	for id := range jobs {
		if sources[id] {
			return errors.New("ASSEMBLE child is also an inventory source")
		}
	}
	return nil
}
