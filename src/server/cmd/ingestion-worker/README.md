# src/server/cmd/ingestion-worker

Entry point daemon coordinator ingestion Go. Proses ini membuka pool PostgreSQL dan client gRPC Rust sekali, mengambil lease job secara berurutan, lalu menjalankan workflow PARSE durable. Ia tidak menjalankan parser sendiri dan tidak melakukan publication snapshot.

Listener Rust masih dibatasi loopback tanpa TLS, sehingga endpoint daemon ini juga wajib berupa host loopback. Migrasi database dijalankan sebagai langkah operasional terpisah sebelum daemon start. Shutdown menghentikan claim baru, membatalkan RPC aktif melalui context, meneruskan cancellation ke worker, dan menutup client/pool.

Ukur queue latency, waktu RPC/checkpoint, retry rate, cancellation lag, idle polling, dan throughput pada workload `configs/benchmark-targets.yaml`. Target tetap **REQUIRED_UNMEASURED**; daemon correctness tidak membuktikan target performa atau kualitas hukum.

