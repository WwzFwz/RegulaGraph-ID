// Mendefinisikan kandidat bukti dengan teks sumber, versi, asal retriever, skor, serta jalur graph.
//
// Peran dalam komponen:
// Menjadi kontrak bersama retrieval, fusion, reranking, dan answering.
//
// Kontrak integrasi dan perhatian implementasi:
// Jangan menghilangkan provenance saat deduplikasi; skor beda retriever tidak diasumsikan satu skala.
//
// Benchmark dan gate penerimaan:
// [DOMAIN] Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.
//
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package domain
