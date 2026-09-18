// Menyediakan adapter inference skor pasangan pertanyaan-teks.
//
// Peran dalam komponen:
// Mendukung src/server/internal/retrieval/reranking.go tanpa menentukan kandidat akhir di adapter.
//
// Kontrak integrasi dan perhatian implementasi:
// Catat model, max length, precision, batch size, dan truncation; skor bukan confidence jawaban.
//
// Benchmark dan gate penerimaan:
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
//
// [RERANK] Bandingkan nDCG@k dan kelengkapan bukti sebelum/sesudah reranking; ukur p50/p95, batch size, panjang pasangan, dan truncation. Kandidat yang hilang sebelum reranking tidak dapat dipulihkan. Target mutu dan budget latency wajib mengikuti configs/benchmark-targets.yaml; perubahan memerlukan persetujuan pengguna.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//
// Batas runtime: file Go ini adalah client inference. Eksekusi tensor berada di src/inference atau provider eksternal; tidak ada pemuatan model lokal di handler Go.
package inference
