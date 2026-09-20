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
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Implement BM25 query path using the pinned analyzer/statistics generation; keep learned sparse as a distinct representation.
// Bukti verifikasi: Test exact legal identifiers, typo/code-switch strata and empty queries; evaluate recall and latency without merging score scales.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package retrieval
