//! Menampung spesifikasi prompt dan identitas versinya untuk tugas ekstraksi.
//!
//! Peran dalam komponen:
//! Membuat instruksi ekstraksi dapat direproduksi dan dievaluasi terpisah dari adapter model.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Prompt aktual belum ditetapkan. Instruksi hanya entitas sentral dapat melewatkan rujukan penting; ukur pengaruhnya terhadap recall.
//!
//! Benchmark dan gate penerimaan:
//! [EXTRACTION] Ukur precision/recall/F1 entitas dan relasi dengan predicate, arah, kondisi, serta bukti sumber; catat kegagalan schema, token, biaya/dokumen, dan waktu p50/p95. Target wajib mengikuti configs/benchmark-targets.yaml dan memerlukan gold set valid; schema valid tidak dianggap fakta benar.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//! Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
//! Version templates and structured-output instructions against the ontology; include primary spans and treat document content as data.
//! Bukti verifikasi: Test schema-invalid output and injected source instructions; record prompt hash and compare extraction quality on frozen dev splits.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
