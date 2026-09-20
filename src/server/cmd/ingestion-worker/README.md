# src/server/cmd/ingestion-worker

Entry point daemon coordinator ingestion Go. Proses ini membuka pool PostgreSQL, shared FileStore, dan client gRPC Rust sekali, lalu menjalankan workflow PARSE, STRUCTURE, BIND, dan CHUNK durable. Executor dokumen merotasi PARSE/STRUCTURE/CHUNK, sedangkan loop luar bergantian dengan BIND agar backlog awal tidak membuat binding kelaparan. Ia tidak menjalankan parser sendiri dan tidak melakukan publication snapshot.

Listener Rust masih dibatasi loopback tanpa TLS, sehingga endpoint daemon ini juga wajib berupa host loopback. Migrasi database dijalankan sebagai langkah operasional terpisah sebelum daemon start. Shutdown menghentikan claim baru, membatalkan RPC aktif melalui context, meneruskan cancellation ke worker, dan menutup client/pool.

Retry transient dijadwalkan durable dengan exponential backoff dari `REGULAGRAPH_COORDINATOR_RETRY_BASE` sampai `REGULAGRAPH_COORDINATOR_RETRY_MAX`. Schema membatasi delapan attempt secara default; perubahan budget per workload memerlukan migration/config contract yang eksplisit. Error input/protokol/integritas yang deterministik langsung menjadi `FAILED` dan tidak menghabiskan antrean lewat retry identik.

BIND membaca tepat satu artefak STRUCTURE terverifikasi dengan batas byte/record, menyelesaikan issuer, regulasi, dan pasal melalui batch registry deterministik maksimum 10.000 claim, lalu menyimpan output content-addressed, dependency evidence, dan terminal checkpoint. Output lengkap menjadi `STAGED` untuk CHUNK; output parsial menjadi `WAITING_REVIEW`. Build ID, yurisdiksi, batas batch, dan ukuran registry batch wajib berasal dari konfigurasi lingkungan.

CHUNK hanya menerima checkpoint BIND terminal sukses. Coordinator memverifikasi ulang bytes, corpus, stage-owned records, completeness, dan producer manifest output sebelum mendaftarkan dependency evidence dan checkpoint. Hasil sukses kembali `STAGED`; worker Rust sudah dapat menjalankan EXTRACT, sedangkan claim/verifikasi/commit output EXTRACT oleh daemon Go belum terhubung.

Ukur queue latency, waktu RPC/checkpoint, retry rate, cancellation lag, idle polling, dan throughput pada workload `configs/benchmark-targets.yaml`. Target tetap **REQUIRED_UNMEASURED**; daemon correctness tidak membuktikan target performa atau kualitas hukum.
