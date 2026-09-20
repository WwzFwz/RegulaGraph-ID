//! Mendeteksi sumber baru, berubah, dan dihapus melalui identitas sumber serta hash konten.
//!
//! Peran dalam komponen:
//! Menjadi pemicu workflow update dan perencanaan invalidasi artefak turunannya.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Perubahan parser/model/prompt/schema juga dapat memerlukan reprocessing walaupun konten sama; hilangnya sumber dari listing belum tentu berarti dicabut.
//!
//! Benchmark dan gate penerimaan:
//! [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//! Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
//! Compare content and producer/dependency fingerprints, including negative lookup revisions, to emit incremental work plans.
//! Bukti verifikasi: Compare incremental vs clean rebuild on late references/model changes; measure reused work while preserving old/shared evidence.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
