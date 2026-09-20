// Menjadi endpoint liveness dan readiness layanan.
//
// Peran dalam komponen:
// Mendukung diagnosis operasional tanpa menjalankan inference mahal.
//
// Kontrak integrasi dan perhatian implementasi:
// Liveness tidak menunggu semua dependency; readiness memeriksa dependency yang diperlukan dengan timeout terbatas.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Report process liveness separately from backend/model/snapshot readiness without expensive full queries.
// Bukti verifikasi: Test degraded dependencies and drain state; bound probe timeout and exclude secrets from responses.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package routes
