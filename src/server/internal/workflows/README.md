# src/server/internal/workflows

Orchestration alur ingestion, pembaruan incremental, dan tanya jawab. Folder ini mengatur urutan tahap, percabangan, checkpoint, retry, dan pelaporan status. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menjadi tempat implementasi parser, retriever, atau SDK database. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menyuntikkan dependency melalui kontrak yang eksplisit dan membawa run ID serta snapshot ID. Retry harus idempotent, publikasi lintas penyimpanan memakai mekanisme yang dirancang, dan kegagalan parsial tetap terlihat.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [answer.go](answer.go), [ingest.go](ingest.go), [ingest_test.go](ingest_test.go), [update.go](update.go), [parse.go](parse.go), [parse_test.go](parse_test.go), [collect.go](collect.go), [collect_test.go](collect_test.go), [discover.go](discover.go), [discover_test.go](discover_test.go).

collect menjalankan batch acquisition D01 melalui adapter sources dengan deduplikasi URL, jumlah worker terbatas, cancellation, progress, serta hitungan sukses/reuse/gagal. Scheduler pada ingest.go membuat job persisten S01 dan memilih `PARSE` bila semua source sudah berupa blob; URL tetap dimiliki `ACQUIRE`. `parse.go` mengambil claim PARSE lalu handoff STRUCTURE dari checkpoint PARSE, membatasi RPC pada deadline lease, meneruskan cancellation durable, memvalidasi binding output, mendaftarkan artefak, dan menyimpan checkpoint. Hasil PARSE parsial masuk `WAITING_REVIEW`; hasil lengkap bergerak ke STRUCTURE dan kembali `STAGED` sambil menunggu registry/binding ketentuan. [Panduan collector](../../../../doc/acquisition.md) menjelaskan acquisition.

discover.go menyimpan checkpoint discovery.json dan antrean queue.txt setelah setiap halaman baru; satu proses penulis per direktori. Seed diikuti breadth-first dengan batas halaman per run, URL dideduplikasi, error dipertahankan dan dicoba ulang pada run berikutnya. Checkpoint adalah inventaris D01 lokal, bukan durable job produksi. Hasil discover tidak membuktikan ketersediaan PDF atau canonical identity.

## Benchmark dan perhatian performa

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

**ANSWER.** Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Collector D01, kontrak/validator C01, scheduler durable S01, serta dispatch PARSE→STRUCTURE ke worker Rust sudah aktif. Retry transient memakai exponential backoff durable dengan budget terpisah per stage; attempt global dan fence tetap monotonik. Coordinator memproses satu batch per loop dan mensyaratkan timeout call lebih pendek dari lease. Lease renewal batch panjang, jitter retry, registry provision/version, CHUNK dan stage berikutnya, answering, serta benchmark end-to-end masih mengikuti paket berikutnya.

Recovery checkpoint otomatis berlaku untuk PARSE dan STRUCTURE karena checkpoint mengikat artefak, hash, fence, serta terminal outcome worker. Lease yang diambil ulang menyalin checkpoint ke fence baru lalu meneruskan `SUCCEEDED`, `FAILED`, atau `CANCELLED` ke state yang sesuai tanpa menjalankan worker kembali. Checkpoint lama tanpa terminal outcome tidak ditebak dan hanya dapat diulang bila budget attempt masih tersedia.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answer.go](answer.go) | Pin one snapshot, resolve temporal intent, coordinate retrieval/context/generation and validate terminal evidence; propagate cancellation. | Test unavailable dependencies, evidence conflicts and snapshot rollover mid-request; trace queue and stage durations. |
| [collect.go](collect.go) | Retain resumable acquisition; integrate receipts into S01 jobs without changing PDF byte-cap accounting. | Test cancellation and budget stop with concurrent workers; reconcile completed/failed/deferred counts and no dangling producer. |
| [discover.go](discover.go) | Retain durable queue checkpoints; add source-specific discovery only after inspecting real portal layouts. | Test cycles, duplicate seeds, resumed pagination and bounded new-page counts; report coverage gaps explicitly. |
| [ingest.go](ingest.go) dan [parse.go](parse.go) | Pertahankan dispatch serta retry durable PARSE→STRUCTURE; tambahkan lease heartbeat untuk batch panjang, registry binding provision/version, stage CHUNK/I01/K01/X01 berikutnya, dan publication coordinator. | Injeksi crash di register/checkpoint/completion, uji cancellation dan bounded-capacity dispatch dengan PostgreSQL nyata, lalu ukur queue/call p95/p99. |
| [update.go](update.go) | Bangun dependency closure termasuk empty lookup revision; stage replacement dan pertahankan versi/bukti bersama. | Uji source withdrawal, late reference, canonical merge/split dan interrupted reindex; bandingkan dengan clean rebuild. |
