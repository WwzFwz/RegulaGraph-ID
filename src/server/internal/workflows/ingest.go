// Menyusun alur sumber, parsing, chunking, graph engineering, dan indexing.
//
// Peran dalam komponen:
// Menjadi orchestration ingestion yang dipanggil API/job atau CLI. Pada S01 file ini
// merealisasikan scheduling durable; tahap parsing sampai indexing disambungkan bertahap
// oleh I01/K01/X01 tanpa mengganti kontrak job.
//
// Kontrak integrasi dan perhatian implementasi:
// Catat run ID, checkpoint, konfigurasi, dan snapshot; failure parsial tidak membuat corpus
// setengah jadi terlihat lengkap. Hash protobuf deterministik mengikat idempotency key.
// Replay request identik mengembalikan job existing, payload berbeda menghasilkan conflict.
// Worker memakai lease/fence dan hanya checkpoint attempt aktif yang boleh dipersist.
//
// Benchmark dan gate penerimaan:
// [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent;
// dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia.
// Uji retry setelah crash, duplicate delivery, stale response, cancellation sebelum
// publication, queue time, throughput, dan freshness lag.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scheduler S01 aktif untuk submit, claim, renew, checkpoint, dan transisi state.
// Eksekusi stage Rust/model dan handoff batch produksi belum aktif sampai paket dependennya.
// Bukti verifikasi mengikuti doc/verification.md.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type JobStore interface {
	SubmitJob(context.Context, domain.JobIntent) (domain.JobRecord, bool, error)
	ClaimJob(context.Context, string, time.Duration) (domain.JobRecord, error)
	RenewLease(context.Context, string, string, uint64, time.Duration) (time.Time, error)
	SaveCheckpoint(context.Context, *pb.Checkpoint, string) error
	TransitionJob(context.Context, string, string, uint64, pb.JobState, pb.JobState) error
}

type JobScheduler struct {
	store JobStore
}

func NewJobScheduler(store JobStore) (*JobScheduler, error) {
	if store == nil {
		return nil, errors.New("job store is required")
	}
	return &JobScheduler{store: store}, nil
}

func (s *JobScheduler) Submit(ctx context.Context, jobID string, request *pb.IngestionRequest) (domain.JobRecord, bool, error) {
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return domain.JobRecord{}, false, fmt.Errorf("validate ingestion request: %w", err)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return domain.JobRecord{}, false, fmt.Errorf("marshal ingestion request: %w", err)
	}
	requestDigest := sha256.Sum256(payload)
	inputRequest := proto.Clone(request).(*pb.IngestionRequest)
	inputRequest.IdempotencyKey = ""
	inputPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(inputRequest)
	if err != nil {
		return domain.JobRecord{}, false, fmt.Errorf("marshal ingestion fingerprint: %w", err)
	}
	inputDigest := sha256.Sum256(inputPayload)
	return s.store.SubmitJob(ctx, domain.JobIntent{
		JobID:            jobID,
		CorpusID:         request.CorpusId,
		Operation:        request.Operation,
		InitialStage:     pb.JobStage_JOB_STAGE_ACQUIRE,
		InputFingerprint: hex.EncodeToString(inputDigest[:]),
		IdempotencyKey:   request.IdempotencyKey,
		RequestHash:      hex.EncodeToString(requestDigest[:]),
		RequestPayload:   payload,
	})
}

func (s *JobScheduler) Claim(ctx context.Context, ownerID string, leaseDuration time.Duration) (domain.JobRecord, error) {
	return s.store.ClaimJob(ctx, ownerID, leaseDuration)
}

func (s *JobScheduler) Renew(ctx context.Context, jobID, ownerID string, fence uint64, leaseDuration time.Duration) (time.Time, error) {
	return s.store.RenewLease(ctx, jobID, ownerID, fence, leaseDuration)
}

func (s *JobScheduler) Checkpoint(ctx context.Context, checkpoint *pb.Checkpoint, ownerID string) error {
	if err := domain.ValidateWire(checkpoint, domain.DefaultWireLimits); err != nil {
		return fmt.Errorf("validate checkpoint: %w", err)
	}
	return s.store.SaveCheckpoint(ctx, checkpoint, ownerID)
}

func (s *JobScheduler) Transition(ctx context.Context, jobID, ownerID string, fence uint64, expected, next pb.JobState) error {
	if !domain.AllowedJobTransition(expected, next) {
		return errors.New("invalid job state transition")
	}
	return s.store.TransitionJob(ctx, jobID, ownerID, fence, expected, next)
}
