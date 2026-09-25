# src/server/cmd/ingestion-worker

Coordinator memuat ontology JSONC terpin SHA-256 sekali saat startup. EXTRACT mensyaratkan hash yang sama pada request tersimpan dan manifest producer serta memeriksa typed predicate/qualifier sebelum artefak didaftarkan.

Entry point daemon coordinator ingestion Go. Proses ini membuka pool PostgreSQL, shared FileStore, dan client gRPC Rust sekali, lalu menjalankan workflow PARSE, STRUCTURE, BIND, CHUNK, dan EXTRACT durable. [resolution.go](resolution.go) menambahkan client gRPC reusable dan executor RESOLVE secara opt-in. Executor dokumen merotasi PARSE/STRUCTURE/CHUNK/EXTRACT, sedangkan loop luar merotasi kelompok dokumen, BIND, dan RESOLVE yang diaktifkan. Ia tidak menjalankan parser sendiri dan tidak melakukan publication snapshot.

Listener Rust masih dibatasi loopback tanpa TLS, sehingga endpoint daemon ini juga wajib berupa host loopback. Migrasi database dijalankan sebagai langkah operasional terpisah sebelum daemon start. Shutdown menghentikan claim baru, membatalkan RPC aktif melalui context, meneruskan cancellation ke worker, dan menutup client/pool.

Retry transient dijadwalkan durable dengan exponential backoff dari `REGULAGRAPH_COORDINATOR_RETRY_BASE` sampai `REGULAGRAPH_COORDINATOR_RETRY_MAX`. Schema membatasi delapan attempt secara default; perubahan budget per workload memerlukan migration/config contract yang eksplisit. Error input/protokol/integritas yang deterministik langsung menjadi `FAILED` dan tidak menghabiskan antrean lewat retry identik.

BIND membaca tepat satu artefak STRUCTURE terverifikasi dengan batas byte/record, menyelesaikan issuer, regulasi, dan pasal melalui batch registry deterministik maksimum 10.000 claim, lalu menyimpan output content-addressed, dependency evidence, dan terminal checkpoint. Output lengkap menjadi `STAGED` untuk CHUNK; output parsial menjadi `WAITING_REVIEW`. Build ID, yurisdiksi, batas batch, dan ukuran registry batch wajib berasal dari konfigurasi lingkungan.

CHUNK hanya menerima checkpoint BIND terminal sukses. EXTRACT hanya menerima checkpoint CHUNK terminal sukses dan `DocumentBatch` lengkap. Coordinator memverifikasi ulang bytes, corpus, accounting per chunk, provenance, source version, model/prompt pin, exact mention text, boundary UTF-8 evidence, producer manifest, dan dependency sebelum mendaftarkan artefak/checkpoint. Hasil sukses kembali `STAGED`; output gagal tetap membawa checkpoint terminal untuk recovery tanpa sampling ulang.

Ukur queue latency, waktu RPC/checkpoint, retry rate, cancellation lag, idle polling, dan throughput pada workload `configs/benchmark-targets.yaml`. Target tetap **REQUIRED_UNMEASURED**; daemon correctness tidak membuktikan target performa atau kualitas hukum.

RESOLVE membutuhkan migration 0012 dan pin producer/schema/policy sesuai [panduan resolusi](../../../../doc/semantic-resolution.md). Proposal berbukti masuk `WAITING_REVIEW` dengan pointer artefak durable; model tidak diberi kewenangan menyetujui LINK. Input tanpa mention dapat menyelesaikan RESOLVE tanpa inference. [resolution_test.go](resolution_test.go) memeriksa enablement, endpoint loopback, dan rotasi tiga kelompok. Review/resume, CREATE/MERGE/SPLIT, partitioning dan lease heartbeat masih terbuka.
