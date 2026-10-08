// Carries internal review handoffs between the authenticated operator workflow and storage.
// Artifact refs bind the complete queued model exchange; approval is explicit and never
// inferred from confidence. These values are not a new public wire schema. Storage must
// atomically persist the review, existing resolution intent and resume transition. Measure
// lock wait, replay/conflict rates and p95/p99 under configs/benchmark-targets.yaml.
package domain

import pb "regulagraph.local/server/gen/regulagraph/v1"

type SemanticReviewQueue struct {
	JobID, CorpusID, SourceCheckpointID string
	Source, Candidates, Input, Output   *pb.ArtifactRef
}

// SemanticReviewCommit is supplied only by the trusted review workflow after verified reads.
// Actor comes from the authenticated boundary, never the model or an untrusted request body.
type SemanticReviewCommit struct {
	Queue         SemanticReviewQueue
	Actor, Reason string
	Intent        SemanticResolutionIntent
}
