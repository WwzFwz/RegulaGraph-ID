// Mengambil kandidat bukti berdasarkan pencocokan lexical BM25.
//
// Peran dalam komponen:
// Menyediakan jalur pencarian kata dan identitas regulasi untuk fusion.
//
// Kontrak integrasi dan perhatian implementasi:
// Gunakan metadata/snapshot yang sama dengan retriever lain; pertanyaan asli tetap dapat digunakan bila normalisasi merusak nomor.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package retrieval
