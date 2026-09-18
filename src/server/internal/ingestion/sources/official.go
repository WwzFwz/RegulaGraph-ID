// Menyediakan kontrak adapter pengambilan dokumen dari sumber resmi yang nantinya dipilih.
//
// Peran dalam komponen:
// Memisahkan akuisisi jaringan dari parsing dan workflow ingestion.
//
// Kontrak integrasi dan perhatian implementasi:
// Belum ada situs atau crawler aktif. Catat URL asal, waktu observasi, redirect, status unduhan, dan checksum; retry dibatasi.
//
// Benchmark dan gate penerimaan:
// [SOURCE] Ukur keberhasilan akuisisi, bytes/detik, retry, dan waktu p50/p95 per sumber. Gate: konten memiliki hash dan asal yang terlacak; unduhan parsial tidak diterbitkan sebagai sumber lengkap.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package sources
