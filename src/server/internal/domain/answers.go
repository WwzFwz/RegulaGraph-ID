// Mendefinisikan jawaban, pemetaan klaim-citation, keterbatasan bukti, serta metadata eksekusi.
//
// Peran dalam komponen:
// Menghubungkan generation, validation, API, dan evaluasi.
//
// Kontrak integrasi dan perhatian implementasi:
// Representasikan jawaban parsial, bukti tidak cukup, dan konflik tanpa mengarang dukungan sumber.
//
// Benchmark dan gate penerimaan:
// [DOMAIN] Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.
//
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Construct claim/citation/stream state helpers over authoritative wire types; preserve semantic vs transport completion.
// Bukti verifikasi: Test byte-accurate claim spans, unknown evidence refs and exactly one terminal stream event.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package domain
