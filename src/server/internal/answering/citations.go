// Memetakan klaim dan citation pada identitas sumber, pasal, ayat, versi, serta rentang teks.
//
// Peran dalam komponen:
// Membuat jawaban dapat ditelusuri oleh API, pengguna, dan evaluator.
//
// Kontrak integrasi dan perhatian implementasi:
// Referensi ada belum tentu mendukung klaim; simpan mapping deterministik dan hindari citation bebas yang tidak tercantum dalam evidence.
//
// Benchmark dan gate penerimaan:
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package answering
