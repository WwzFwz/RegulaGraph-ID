// Defines the cross-component input to a fenced semantic registry write.
// Workflows obtain immutable bytes through ReadVerified and carry the claimed job's
// lease/checkpoint identity; PostgreSQL authenticates that identity inside the CAS transaction.
// ReviewedLink is only an assertion: an authenticated review producer must persist the matching
// review row before LINK. Measure input validation, storage reads, lock wait, and commit p95/p99;
// required targets in configs/benchmark-targets.yaml remain REQUIRED_UNMEASURED.
package domain

import pb "regulagraph.local/server/gen/regulagraph/v1"

type SemanticJobFence struct {
	JobID              string
	OwnerID            string
	SourceCheckpointID string
	Fence              uint64
}

type SemanticRegistryInputs struct {
	SourceRef      *pb.ArtifactRef
	SourceBytes    []byte
	CandidateRef   *pb.ArtifactRef
	CandidateBytes []byte
}

type ReviewedLink struct {
	ProposalID  string
	CanonicalID string
	Actor       string
	Reason      string
	ReviewID    string
}

// SemanticResolutionIntent is the immutable input needed to replay a registry commit after a
// crash before RESOLVE checkpoint publication. Its preview was validated before CAS; PostgreSQL
// persists it under the EXTRACT job fence, then reloads and checks its hash on retry.
type SemanticResolutionIntent struct {
	SourceCheckpointID string
	CandidateRef       *pb.ArtifactRef
	Request            *pb.RegistryResolveRequest
	Approvals          []ReviewedLink
	Preview            *pb.ResolutionBatch
}
