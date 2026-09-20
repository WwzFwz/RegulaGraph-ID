//! Mendefinisikan mention, canonical entity, alias, dan riwayat keputusan penyamaan identitas.
//!
//! Peran dalam komponen:
//! Menjadi kontrak extraction, resolution, assembly, serta query linking.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Nama yang sama belum tentu identitas sama; alias diberi konteks dan tipe, canonical ID tidak berubah hanya karena label tampil berubah.
//!
//! Benchmark dan gate penerimaan:
//! [DOMAIN] Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.
//!
//! [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//!
//! Kontrak wire bersama berasal dari src/contracts. Tipe Rust nantinya hanya representasi lokal; field wire tidak didefinisikan ulang secara independen.
//! Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
//! Expose scoped canonical identity/revision and resolution decision helpers without autonomous registry writes.
//! Bukti verifikasi: Test alias ambiguity, merge/split lineage and deterministic identity comparison; avoid redundant conversion/allocation across batches.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
