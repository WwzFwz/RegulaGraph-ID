# src/server/internal/workflows

Orchestration alur ingestion, pembaruan incremental, dan tanya jawab. Folder ini mengatur urutan tahap, percabangan, checkpoint, retry, dan pelaporan status. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menjadi tempat implementasi parser, retriever, atau SDK database. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menyuntikkan dependency melalui kontrak yang eksplisit dan membawa run ID serta snapshot ID. Retry harus idempotent, publikasi lintas penyimpanan memakai mekanisme yang dirancang, dan kegagalan parsial tetap terlihat.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [answer.go](answer.go), [ingest.go](ingest.go), [update.go](update.go), [collect.go](collect.go), [collect_test.go](collect_test.go), [discover.go](discover.go), [discover_test.go](discover_test.go).

collect menjalankan batch acquisition D01 melalui adapter sources dengan deduplikasi URL, jumlah worker terbatas, cancellation, progress, serta hitungan sukses/reuse/gagal. Ia belum membuat job persisten, memublikasikan snapshot, atau menjalankan pipeline pada ingest/update/answer yang masih scaffold. [Panduan collector](../../../../doc/acquisition.md) menjelaskan cara menjalankannya.

discover.go menyimpan checkpoint discovery.json dan antrean queue.txt setelah setiap halaman baru; satu proses penulis per direktori. Seed diikuti breadth-first dengan batas halaman per run, URL dideduplikasi, error dipertahankan dan dicoba ulang pada run berikutnya. Checkpoint adalah inventaris D01 lokal, bukan durable job produksi. Hasil discover tidak membuktikan ketersediaan PDF atau canonical identity.

## Benchmark dan perhatian performa

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

**ANSWER.** Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../../../../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answer.go](answer.go) | Pin one snapshot, resolve temporal intent, coordinate retrieval/context/generation and validate terminal evidence; propagate cancellation. | Test unavailable dependencies, evidence conflicts and snapshot rollover mid-request; trace queue and stage durations. |
| [collect.go](collect.go) | Retain resumable acquisition; integrate receipts into S01 jobs without changing PDF byte-cap accounting. | Test cancellation and budget stop with concurrent workers; reconcile completed/failed/deferred counts and no dangling producer. |
| [discover.go](discover.go) | Retain durable queue checkpoints; add source-specific discovery only after inspecting real portal layouts. | Test cycles, duplicate seeds, resumed pagination and bounded new-page counts; report coverage gaps explicitly. |
| [ingest.go](ingest.go) | Schedule typed worker stages with idempotency keys, leases/fences, checkpoints and bounded capacity; hand validated batches to publisher. | Test retry after crash, duplicate delivery, stale worker response and cancellation before publication with real storage. |
| [update.go](update.go) | Build dependency closure including empty lookup revisions; stage replacements and publish while retaining old versions and shared supports. | Test source withdrawal, late references, canonical merge/split and interrupted reindex; compare incremental output to clean rebuild. |
