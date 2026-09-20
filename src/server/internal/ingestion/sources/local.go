// Membaca dokumen lokal beserta metadata asal dan hash kontennya.
//
// Peran dalam komponen:
// Menyediakan input yang dapat diulang untuk ingestion tanpa mengikat parser pada filesystem.
//
// Kontrak integrasi dan perhatian implementasi:
// Validasi path dan file; jangan menganggap filename sebagai ID regulasi atau tanggal modifikasi sebagai tanggal berlaku.
//
// Benchmark dan gate penerimaan:
// [SOURCE] Ukur keberhasilan akuisisi, bytes/detik, retry, dan waktu p50/p95 per sumber. Gate: konten memiliki hash dan asal yang terlacak; unduhan parsial tidak diterbitkan sebagai sumber lengkap.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Import explicitly selected local files through the same hash/receipt contracts as official acquisition.
// Bukti verifikasi: Test invalid paths, corrupt/duplicate PDFs and interruption; avoid interpreting file timestamps as legal effective dates.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package sources
