// Mendefinisikan endpoint, predicate, arah, kondisi, versi, dan bukti untuk setiap relasi.
//
// Peran dalam komponen:
// Menjaga kesesuaian output extraction, assembly, penyimpanan graph, dan traversal.
//
// Kontrak integrasi dan perhatian implementasi:
// Relasi dapat didukung beberapa sumber; explicit/inferred dan status validasi dibedakan. Confidence model bukan probabilitas terkalibrasi.
//
// Benchmark dan gate penerimaan:
// [DOMAIN] Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.
//
// [EXTRACTION] Ukur precision/recall/F1 entitas dan relasi dengan predicate, arah, kondisi, serta bukti sumber; catat kegagalan schema, token, biaya/dokumen, dan waktu p50/p95. Target wajib mengikuti configs/benchmark-targets.yaml dan memerlukan gold set valid; schema valid tidak dianggap fakta benar.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package domain
