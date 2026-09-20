// Menghasilkan representasi pencarian pertanyaan sambil mempertahankan versi aslinya.
//
// Peran dalam komponen:
// Membantu pencarian typo, bahasa informal, dan code-switch tanpa mengubah maksud.
//
// Kontrak integrasi dan perhatian implementasi:
// Jangan mengubah nomor/tahun, negasi, atau filter waktu; normalisasi opsional dievaluasi per kategori pertanyaan.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Preserve original question and normalize mechanical variants while keeping negation, article numbers, years and quoted terms.
// Bukti verifikasi: Test informal/typo/Indonesian-English cases and destructive normalization counterexamples; record original-to-normalized trace.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package query
