// Menyusun instance adapter, konfigurasi, dan workflow bagi request aplikasi.
//
// Peran dalam komponen:
// Menjadi composition root sehingga route dapat diuji dengan dependency pengganti.
//
// Kontrak integrasi dan perhatian implementasi:
// Hindari membuat model/koneksi baru per request; perhatikan lifecycle, thread safety, dan batas concurrency.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
//
// [STORAGE] Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.
//
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Construct and close shared pools/clients explicitly; inject narrow workflow interfaces and pin model/config manifests.
// Bukti verifikasi: Test partial startup cleanup and dependency failure without opening connections during package initialization.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package api
