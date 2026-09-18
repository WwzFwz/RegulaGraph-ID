//! Menentukan mention yang menunjuk entitas sama dan keputusan canonical yang terlacak.
//!
//! Peran dalam komponen:
//! Menghubungkan fakta lintas dokumen tanpa mencampur entitas berbeda.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Gunakan tipe, konteks, dan identitas peraturan; mention yang belum terselesaikan tetap disimpan. Merge dapat dikoreksi dengan riwayat dependensi.
//!
//! Benchmark dan gate penerimaan:
//! [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
