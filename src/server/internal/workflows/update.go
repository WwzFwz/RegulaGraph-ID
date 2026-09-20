// Mengoordinasikan pembaruan incremental dan invalidasi seluruh artefak terdampak.
//
// Peran dalam komponen:
// Menjaga konsistensi metadata, graph, ringkasan, dan indeks setelah perubahan sumber.
// Pada S01 boundary ini memastikan endpoint update hanya menjadwalkan JOB_OPERATION_UPDATE
// dengan idempotency dan fencing yang sama seperti ingestion.
//
// Kontrak integrasi dan perhatian implementasi:
// Perhitungkan dependensi lintas dokumen dan perubahan model/schema; gunakan staging dan
// checkpoint sebelum publikasi, bukan asumsi transaksi lintas database. Dependency closure,
// empty lookup revision, selective reuse, serta full-rebuild equivalence diselesaikan U01.
//
// Benchmark dan gate penerimaan:
// [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent;
// dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia.
// Uji source withdrawal, late reference, canonical merge/split, interrupted reindex, dan
// bandingkan incremental output dengan clean rebuild pada manifest sama.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: boundary submit update S01 aktif; perhitungan closure dan eksekusi incremental
// menyeluruh belum aktif sampai U01. Bukti verifikasi mengikuti doc/verification.md.
package workflows

import (
	"context"
	"errors"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func (s *JobScheduler) SubmitUpdate(ctx context.Context, jobID string, request *pb.IngestionRequest) (domain.JobRecord, bool, error) {
	if request == nil || request.Operation != pb.JobOperation_JOB_OPERATION_UPDATE {
		return domain.JobRecord{}, false, errors.New("update workflow requires JOB_OPERATION_UPDATE")
	}
	return s.Submit(ctx, jobID, request)
}
