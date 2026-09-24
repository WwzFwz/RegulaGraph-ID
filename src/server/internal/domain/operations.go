// Menyediakan tipe domain untuk job durable, publication reservation, dan snapshot pin.
// Peran: menjadi boundary stabil antara workflow/indexing dan adapter storage sehingga
// dependency mengarah ke domain, bukan dari orchestration ke implementasi PostgreSQL.
// Kontrak: enum mengikuti protobuf C01, timestamp adalah instant UTC, identifier/fence
// divalidasi adapter pemilik, dan tipe ini tidak melakukan I/O atau membuka koneksi.
// Benchmark: record kecil ini tidak membawa payload dokumen; allocation/serialization
// diukur bersama operasi storage pemiliknya.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: tipe boundary S01 aktif untuk scheduler dan publication coordinator.
package domain

import (
	"errors"
	"time"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ErrPersistentIntegrity marks durable bytes/state that cannot succeed on an identical retry.
// Workflows use it to quarantine or fail the job while transport and database availability errors retry.
var ErrPersistentIntegrity = errors.New("persistent integrity violation")

// ErrResolutionReplan marks a verified RESOLVE intent whose pinned registry view can no
// longer commit. The same job must fail; a new job may build candidates at a fresh revision.
var ErrResolutionReplan = errors.New("semantic resolution requires a new candidate plan")

// ErrCandidateViewChanged means the registry advanced during candidate preparation,
// before an immutable RESOLVE intent exists. The same claimed job may refresh its view.
var ErrCandidateViewChanged = errors.New("candidate registry view changed; retry lookup")

// ErrLeaseUnavailable is shared by schedulers and storage adapters when no durable job is due.
var ErrLeaseUnavailable = errors.New("no claimable job")

// ErrNotFound identifies durable prerequisites that are absent, distinct from backend availability.
var ErrNotFound = errors.New("record not found")

type JobIntent struct {
	JobID            string
	CorpusID         string
	Operation        pb.JobOperation
	InitialStage     pb.JobStage
	InputFingerprint string
	IdempotencyKey   string
	RequestHash      string
	RequestPayload   []byte
	BaseSnapshotID   string
}

type JobRecord struct {
	JobID                 string
	CorpusID              string
	Operation             pb.JobOperation
	State                 pb.JobState
	Stage                 pb.JobStage
	InputFingerprint      string
	IdempotencyKey        string
	RequestHash           string
	BaseSnapshotID        string
	LatestCheckpointID    string
	Attempt               uint32
	StageAttempt          uint32
	LeaseOwner            string
	LeaseFence            uint64
	LeaseExpiresAt        time.Time
	CancellationRequested bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type PublicationReservation struct {
	PublicationID    string
	JobID            string
	CorpusID         string
	SnapshotID       string
	ParentSnapshotID string
	Sequence         uint64
	Fence            uint64
	State            pb.SnapshotState
}

type SnapshotPin struct {
	LeaseID    string
	CorpusID   string
	SnapshotID string
	Sequence   uint64
	OwnerID    string
	ExpiresAt  time.Time
}

// AllowedJobTransition covers worker/workflow-owned transitions only. Publication commit
// is the sole owner of SUCCEEDED, and PUBLISHING termination must commit or compensate.
func AllowedJobTransition(from, to pb.JobState) bool {
	switch from {
	case pb.JobState_JOB_STATE_RUNNING:
		return to == pb.JobState_JOB_STATE_WAITING_REVIEW || to == pb.JobState_JOB_STATE_STAGED ||
			to == pb.JobState_JOB_STATE_RETRY_WAIT || to == pb.JobState_JOB_STATE_FAILED ||
			to == pb.JobState_JOB_STATE_CANCELLED
	case pb.JobState_JOB_STATE_STAGED:
		return to == pb.JobState_JOB_STATE_VALIDATING || to == pb.JobState_JOB_STATE_FAILED
	case pb.JobState_JOB_STATE_VALIDATING:
		return to == pb.JobState_JOB_STATE_PUBLISHING || to == pb.JobState_JOB_STATE_FAILED
	default:
		return false
	}
}

// JobStageRank defines pipeline order independently from append-only protobuf enum numbers.
// BIND and CHUNK were added after the original EXTRACT→INDEX values, so numeric comparison would
// reject valid CHUNK→EXTRACT checkpoints and must never own progression semantics.
func JobStageRank(stage pb.JobStage) (int, bool) {
	switch stage {
	case pb.JobStage_JOB_STAGE_ACQUIRE:
		return 1, true
	case pb.JobStage_JOB_STAGE_PARSE:
		return 2, true
	case pb.JobStage_JOB_STAGE_STRUCTURE:
		return 3, true
	case pb.JobStage_JOB_STAGE_BIND:
		return 4, true
	case pb.JobStage_JOB_STAGE_CHUNK:
		return 5, true
	case pb.JobStage_JOB_STAGE_EXTRACT:
		return 6, true
	case pb.JobStage_JOB_STAGE_RESOLVE:
		return 7, true
	case pb.JobStage_JOB_STAGE_ASSEMBLE:
		return 8, true
	case pb.JobStage_JOB_STAGE_INDEX:
		return 9, true
	default:
		return 0, false
	}
}

// JobStagePrecedesOrEquals rejects unspecified/unknown values as well as semantic regressions.
func JobStagePrecedesOrEquals(left, right pb.JobStage) bool {
	leftRank, leftValid := JobStageRank(left)
	rightRank, rightValid := JobStageRank(right)
	return leftValid && rightValid && leftRank <= rightRank
}
