//! Membentuk kelompok kandidat penyamaan identitas dengan sinyal murah sebelum keputusan resolver.
//!
//! Peran dalam komponen:
//! Mengendalikan biaya resolution pada kumpulan mention besar.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Blocking bukan keputusan merge; alias lintas singkatan yang tidak berbagi token memerlukan jalur kandidat lain.
//!
//! Benchmark dan gate penerimaan:
//! [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//! Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
//! Retrieve bounded canonical candidates using deterministic legal keys and contextual signals before expensive resolution.
//! Bukti verifikasi: Measure candidate recall and reduction ratio on hard aliases; record empty lookup scopes for later invalidation.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.
