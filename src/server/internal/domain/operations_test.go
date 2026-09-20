// Verifies durable pipeline ordering independently from append-only protobuf enum numbers.
// These tests protect checkpoint fencing when BIND/CHUNK transition into older EXTRACT–INDEX
// values. They are correctness guards; queue latency and throughput remain REQUIRED_UNMEASURED.
package domain

import (
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestJobStageRankFollowsPipelineInsteadOfEnumNumber(t *testing.T) {
	ordered := []pb.JobStage{
		pb.JobStage_JOB_STAGE_ACQUIRE,
		pb.JobStage_JOB_STAGE_PARSE,
		pb.JobStage_JOB_STAGE_STRUCTURE,
		pb.JobStage_JOB_STAGE_BIND,
		pb.JobStage_JOB_STAGE_CHUNK,
		pb.JobStage_JOB_STAGE_EXTRACT,
		pb.JobStage_JOB_STAGE_RESOLVE,
		pb.JobStage_JOB_STAGE_ASSEMBLE,
		pb.JobStage_JOB_STAGE_INDEX,
	}
	for index, stage := range ordered {
		rank, valid := JobStageRank(stage)
		if !valid || rank != index+1 {
			t.Fatalf("stage %s rank=%d valid=%t, want %d", stage, rank, valid, index+1)
		}
		if index > 0 && !JobStagePrecedesOrEquals(ordered[index-1], stage) {
			t.Fatalf("valid transition %s→%s was treated as regression", ordered[index-1], stage)
		}
	}
	if JobStagePrecedesOrEquals(pb.JobStage_JOB_STAGE_CHUNK, pb.JobStage_JOB_STAGE_BIND) {
		t.Fatal("CHUNK→BIND regression was accepted")
	}
	if !JobStagePrecedesOrEquals(pb.JobStage_JOB_STAGE_CHUNK, pb.JobStage_JOB_STAGE_EXTRACT) {
		t.Fatal("append-only enum numbers broke CHUNK→EXTRACT ordering")
	}
	if _, valid := JobStageRank(pb.JobStage_JOB_STAGE_UNSPECIFIED); valid ||
		JobStagePrecedesOrEquals(pb.JobStage_JOB_STAGE_UNSPECIFIED, pb.JobStage_JOB_STAGE_PARSE) {
		t.Fatal("unspecified stage was accepted")
	}
}
