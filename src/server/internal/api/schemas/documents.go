// Mendefinisikan request ingestion dan response status dokumen/job.
//
// Peran dalam komponen:
// Memisahkan penerimaan dokumen dari pekerjaan pemrosesan yang panjang.
//
// Kontrak integrasi dan perhatian implementasi:
// Validasi ukuran/format dan identitas job; status accepted tidak berarti indeks sudah tersedia untuk retrieval.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
//
// [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Map document/version/job payloads without leaking SDK objects or storage paths.
// Bukti verifikasi: Test historical versions, paging cursors and failed/partial job responses using contract fixtures.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package schemas
