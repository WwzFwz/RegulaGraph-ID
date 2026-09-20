// Mendefinisikan request pertanyaan dan response jawaban beserta citation.
//
// Peran dalam komponen:
// Memetakan transport HTTP ke domain query/answer tanpa objek vendor.
//
// Kontrak integrasi dan perhatian implementasi:
// Tanggal acuan, batas corpus, pertanyaan asli, status bukti, dan ID sumber perlu eksplisit; jangan memaksakan field yang belum diputuskan.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
//
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Map public JSON to authoritative QuestionRequest/Answer/Event types with explicit optional/date/uint64 handling.
// Bukti verifikasi: Round-trip null/absent fields and stream error/final cases against contracts; reject unsupported enums.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package schemas
