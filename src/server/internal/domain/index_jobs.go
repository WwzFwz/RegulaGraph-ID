// Defines the coordinator-owned inventory for durable initial INDEX child jobs.
// Existing C01 plans/references remain the wire source of truth; these local
// records bind one plan to one job and one publication fence. Storage must admit
// the entire inventory atomically and preserve it across retries. No model or
// database I/O occurs here. Measure scheduling/claim latency and recovery under
// configs/benchmark-targets.yaml; successful validation is not publication.
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type IndexJobAssignment struct {
	JobID       string
	SourceJobID string
	Plan        *pb.IndexBuildPlan
	Reference   *pb.ArtifactRef
}

type IndexJobInventory struct {
	Binding     IndexCatalogBinding
	Snapshot    *pb.SnapshotRef
	AuthScope   string
	Assignments []IndexJobAssignment
}

func ValidateIndexJobInventory(in IndexJobInventory) error {
	if err := ValidateIndexCatalogBinding(in.Binding); err != nil {
		return err
	}
	if err := ValidateWire(in.Snapshot, DefaultWireLimits); err != nil {
		return err
	}
	if in.AuthScope == "" || len(in.AuthScope) > 256 || in.Snapshot.CorpusId != in.Binding.Generation.Meta.CorpusId || in.Snapshot.RepresentationGeneration != in.Binding.Generation.Meta.RecordId || len(in.Assignments) == 0 || len(in.Assignments) > 256 {
		return errors.New("invalid INDEX inventory identity/bounds")
	}
	jobs, plans, chunks := map[string]bool{}, map[string]bool{}, map[string]bool{}
	records, outputs := map[string]bool{}, map[string]bool{}
	sources := map[string]*pb.ArtifactRef{}
	owners := map[string]string{}
	total := 0
	for _, a := range in.Assignments {
		for _, id := range []string{a.JobID, a.SourceJobID} {
			if err := ValidateWire(&pb.RecordMeta{SchemaVersion: 1, CorpusId: in.Snapshot.CorpusId, RecordId: id}, DefaultWireLimits); err != nil {
				return err
			}
		}
		if err := ValidateIndexBuildPlan(a.Plan); err != nil {
			return err
		}
		if err := ValidateWire(a.Reference, DefaultWireLimits); err != nil {
			return err
		}
		if jobs[a.JobID] || plans[a.Reference.ArtifactId] || a.JobID == a.SourceJobID || a.Reference.MediaType != IndexBuildPlanMediaType || a.Plan.Meta.RecordId != a.Reference.ArtifactId || !proto.Equal(a.Plan.Generation, in.Binding.Generation) || !proto.Equal(a.Plan.TargetSnapshot, in.Snapshot) || !proto.Equal(a.Plan.SourceSnapshot, in.Snapshot) || len(a.Plan.Closures) > 0 {
			return errors.New("INDEX inventory contains conflicting plan/job/snapshot")
		}
		if !proto.Equal(a.Plan.Producer, in.Assignments[0].Plan.Producer) || outputs[a.Plan.OutputBatchId] {
			return errors.New("INDEX inventory producer/output identity conflict")
		}
		if previous := sources[a.SourceJobID]; previous != nil && !proto.Equal(previous, a.Plan.DocumentBatch) {
			return errors.New("INDEX source job refers to different artifacts")
		}
		if owner := owners[a.Plan.DocumentBatch.ArtifactId]; owner != "" && owner != a.SourceJobID {
			return errors.New("INDEX source artifact has different owners")
		}
		sources[a.SourceJobID] = a.Plan.DocumentBatch
		owners[a.Plan.DocumentBatch.ArtifactId] = a.SourceJobID
		outputs[a.Plan.OutputBatchId] = true
		raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(a.Plan)
		if err != nil {
			return err
		}
		total += len(raw)
		if total > 64<<20 || uint64(len(raw)) != a.Reference.ByteSize || fmt.Sprintf("%x", sha256.Sum256(raw)) != a.Reference.ContentHash.Sha256 {
			return errors.New("INDEX inventory plan bytes differ from reference")
		}
		jobs[a.JobID] = true
		plans[a.Reference.ArtifactId] = true
		for _, item := range a.Plan.Items {
			if chunks[item.ChunkId] || records[item.RecordId] {
				return errors.New("INDEX inventory repeats a chunk")
			}
			chunks[item.ChunkId] = true
			records[item.RecordId] = true
		}
	}
	for id := range jobs {
		if sources[id] != nil {
			return errors.New("INDEX child job cannot also be an inventory source")
		}
	}
	return nil
}
