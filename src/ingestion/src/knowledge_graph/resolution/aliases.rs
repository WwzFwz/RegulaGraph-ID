//! Mengelola pemetaan alias dan mention ke canonical ID dengan konteksnya.
//!
//! Peran dalam komponen:
//! Mendukung assembly dan query linking memakai identitas bersama.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Jangan gunakan satu dictionary global nama-ke-ID tanpa ruang ambiguitas; label identik bisa merujuk beberapa entitas.
//!
//! Benchmark dan gate penerimaan:
//! [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
