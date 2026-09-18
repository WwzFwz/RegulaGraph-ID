// Menggabungkan daftar kandidat dari jalur lexical, dense, dan graph.
//
// Peran dalam komponen:
// Menghasilkan kandidat bersama dengan provenance dan ranking asal yang masih terlacak.
//
// Kontrak integrasi dan perhatian implementasi:
// RRF dapat menjadi baseline eksperimen; jangan menjumlahkan raw score yang berbeda skala. Deduplikasi memakai identitas bukti/versi.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package retrieval
