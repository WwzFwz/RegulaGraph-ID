// Mendefinisikan chunk, lokasi teks, hitungan token, serta hubungan ke induk dan versi ketentuan.
//
// Peran dalam komponen:
// Menghubungkan hasil chunking dengan indeks, retrieval, dan context builder.
//
// Kontrak integrasi dan perhatian implementasi:
// Chunk tidak menjadi identitas entitas; rentang sumber asli tetap dapat ditelusuri setelah normalisasi.
//
// Benchmark dan gate penerimaan:
// [DOMAIN] Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.
//
// [CHUNKING] Ukur cakupan pemetaan chunk ke sumber/induk, proporsi ketentuan-pengecualian yang terpisah, Recall@k bukti, jumlah token, serta waktu ingestion. Gate: tiap chunk terbit punya parent/source/version yang valid. Bandingkan struktur+konteks induk dengan baseline pada corpus yang sama; ambang kualitas/latency wajib mengikuti configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Construct chunk and parent views retaining provision version, source spans and tokenizer identity.
// Bukti verifikasi: Test missing parents, split Unicode and overlong units without source loss; avoid redundant conversion/allocation across batches.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package domain
