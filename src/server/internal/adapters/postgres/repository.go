// Menyediakan adapter penyimpanan metadata, versi, dan manifest eksekusi.
//
// Peran dalam komponen:
// Melaksanakan kebutuhan persistensi komponen tanpa membawa logika domain ke SQL client.
//
// Kontrak integrasi dan perhatian implementasi:
// Gunakan transaksi lokal, constraint ID, pool, dan query berparameter; schema aktual dan driver belum ditetapkan.
//
// Benchmark dan gate penerimaan:
// [STORAGE] Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.
//
// [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package postgres
