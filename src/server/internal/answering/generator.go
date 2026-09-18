// Menghasilkan jawaban Bahasa Indonesia berdasarkan konteks bukti dan pertanyaan.
//
// Peran dalam komponen:
// Menghubungkan evidence yang dipersiapkan ke adapter LLM serta domain Answer.
//
// Kontrak integrasi dan perhatian implementasi:
// Prompt actual belum diimplementasikan; dukungan tidak cukup harus dapat menghasilkan jawaban terbatas. Catat model/prompt version dan token.
//
// Benchmark dan gate penerimaan:
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
//
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package answering
