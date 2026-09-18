// Menyusun alur sumber, parsing, chunking, graph engineering, dan indexing.
//
// Peran dalam komponen:
// Menjadi orchestration ingestion awal yang dipanggil API/job atau CLI.
//
// Kontrak integrasi dan perhatian implementasi:
// Catat run ID, checkpoint, konfigurasi, dan snapshot; failure parsial tidak membuat corpus setengah jadi terlihat lengkap.
//
// Benchmark dan gate penerimaan:
// [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package workflows
