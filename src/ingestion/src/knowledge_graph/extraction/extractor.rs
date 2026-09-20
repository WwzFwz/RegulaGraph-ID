//! Mengatur ekstraksi mention dan relasi terstruktur dari konteks dokumen.
//!
//! Peran dalam komponen:
//! Menjadi tahap pertama graph engineering sebelum resolution.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Validasi bentuk output dan referensi sumber secara terpisah dari kebenaran semantik; pertahankan syarat, pengecualian, dan arah relasi.
//!
//! Benchmark dan gate penerimaan:
//! [EXTRACTION] Ukur precision/recall/F1 entitas dan relasi dengan predicate, arah, kondisi, serta bukti sumber; catat kegagalan schema, token, biaya/dokumen, dan waktu p50/p95. Target wajib mengikuti configs/benchmark-targets.yaml dan memerlukan gold set valid; schema valid tidak dianggap fakta benar.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//! Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
//! Extract mentions/assertions with exact evidence spans, typed qualifiers and explicit partial/error status from context batches.
//! Bukti verifikasi: Evaluate precision/recall on human gold including negation/conditions/exceptions; measure cost and throughput without query-time extraction.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
