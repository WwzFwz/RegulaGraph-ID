// Builds a revision-bound graph envelope while retaining original resolver
// proposals, decisions and evidence. The outer registry view is a coordinator
// reaffirmation, not another model decision. Exact source/target/policy hashes
// identify the transform. This pure helper gives no authority: PostgreSQL must
// authenticate historical decisions, unchanged candidate/document dependencies
// and the live target before storing its immutable source receipt. Benchmark
// hash/decode RSS and p95 using benchmark-targets.yaml; quality is unmeasured.
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const GraphSourceReaffirmationPolicy = "graph-source-registry-reaffirmation-v1"

// BindGraphSourceReceiptEnvelopes supports the original immutable binder and an
// explicit cross-revision policy. Original artifacts are never modified.
func BindGraphSourceReceiptEnvelopes(binding GraphSourceBinding, input GraphSourceBindingInputs, maximumEdges int) (GraphSourceArtifact, GraphSourceArtifact, error) {
	fail := func(err error) (GraphSourceArtifact, GraphSourceArtifact, error) {
		return GraphSourceArtifact{}, GraphSourceArtifact{}, err
	}
	if binding.Policy != GraphSourceEnvelopePolicy && binding.Policy != GraphSourceReaffirmationPolicy {
		return fail(errors.New("unknown graph source binding policy"))
	}
	extract, resolve, err := BindGraphSourceEnvelopes(binding.Source.SourceJobID,
		GraphSourceArtifact{Reference: binding.Source.Original, Bytes: input.OriginalDocument}, GraphSourceArtifact{Reference: binding.Source.Bound, Bytes: input.SnapshotDocument},
		GraphSourceArtifact{Reference: binding.OriginalExtraction, Bytes: input.Extraction}, GraphSourceArtifact{Reference: binding.OriginalResolution, Bytes: input.Resolution}, maximumEdges)
	if err != nil {
		return fail(err)
	}
	if binding.Policy == GraphSourceEnvelopePolicy {
		return extract, resolve, nil
	}
	r := new(pb.ResolutionBatch)
	if err = DecodeWire(resolve.Bytes, r, DefaultWireLimits); err != nil {
		return fail(err)
	}
	if binding.RegistryRevision <= r.RegistryRevision || binding.RegistryRevision > 1<<63-1 {
		return fail(errors.New("reaffirmation requires a strictly later retained registry view"))
	}
	// The original receipt and exact snapshot envelope are dependencies already;
	// add a versioned transform fingerprint covering the target view as well.
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d", GraphSourceReaffirmationPolicy, resolve.Reference.ContentHash.Sha256, binding.RegistryRevision))))
	oldRoot := r.Meta.RecordId
	r.Meta.RecordId = "graph-source:reaffirm:" + digest
	r.Context.RequestId, r.Context.TraceId = r.Meta.RecordId, r.Meta.RecordId
	r.Dependencies.ArtifactId = r.Meta.RecordId
	policyID := "policy:" + GraphSourceReaffirmationPolicy
	for _, dependency := range r.Dependencies.Dependencies {
		if dependency.DependencyId == policyID {
			return fail(errors.New("reaffirmation policy dependency collision"))
		}
	}
	r.Dependencies.Dependencies = append(r.Dependencies.Dependencies, &pb.Dependency{DependencyId: policyID, Fingerprint: &pb.ContentHash{Sha256: digest}})
	r.RegistryRevision = binding.RegistryRevision
	for _, issue := range r.Issues {
		for i, id := range issue.EvidenceRefs {
			if id == oldRoot {
				issue.EvidenceRefs[i] = r.Meta.RecordId
			}
		}
	}
	e := new(pb.ExtractionBatch)
	if err = DecodeWire(extract.Bytes, e, DefaultWireLimits); err != nil {
		return fail(err)
	}
	if err = ValidateResolutionBatchClosure(r, e, extract.Reference, maximumEdges); err != nil {
		return fail(err)
	}
	resolve, err = encodeGraphSourceEnvelope(r, resolve.Reference.MediaType, "resolution-batch")
	if err != nil {
		return fail(err)
	}
	if len(extract.Bytes) > DefaultWireLimits.MaxBytes-len(resolve.Bytes) {
		return fail(errors.New("reaffirmed graph envelopes exceed byte budget"))
	}
	// Clone-based decoding above preserves every historic decision revision and
	// model manifest; target revision describes only this derived assembly view.
	return extract, resolve, nil
}

// ResolutionViewCanReaffirm forbids rollback without treating a newer global
// revision as evidence that the old candidate context has changed.
func ResolutionViewCanReaffirm(resolution *pb.ResolutionBatch, target uint64) bool {
	return resolution != nil && resolution.RegistryRevision > 0 && target >= resolution.RegistryRevision && target <= 1<<63-1
}
