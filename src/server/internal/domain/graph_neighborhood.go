// Describes one bounded discovery expansion of a pinned graph. Protobuf records
// remain the source of assertion direction, qualifiers and source provenance;
// this local DTO is not a second wire schema or legal applicability decision.
// ReadBytes accounts protobuf reads (including repeats), not Bolt framing/RSS.
// MoreAssertions reports a truncated frontier; support overflow fails the batch
// rather than return an assertion with silently incomplete source accounting.
package domain

import (
	"errors"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

var ErrGraphReadBudget = errors.New("graph read resource budget exhausted")

type GraphReadLimits struct {
	Assertions int
	Supports   int
	Bytes      uint64
}

type GraphNeighborhood struct {
	Snapshot       *pb.SnapshotRef
	Entities       []*pb.CanonicalEntity
	Assertions     []*pb.RelationAssertion
	Supports       []*pb.SupportRecord
	MoreAssertions bool
	ReadBytes      uint64
}
