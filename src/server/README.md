# src/server

Modul Go untuk API, CLI operasional, orchestration query dan ingestion, retrieval, answering, serta adapter database. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../AGENTS.md).

## Peran dan integrasi anak

Fusion, filtering, context builder, dan citation tetap satu proses. Coordinator memiliki penjadwalan serta publikasi snapshot; Rust menghasilkan batch artefak dan C++ menjalankan inference.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [cmd/](cmd/README.md), [internal/](internal/README.md).

Berkas: [go.mod](go.mod), [go.sum](go.sum).

CLI discover/collect/audit sudah menjalankan acquisition D01. Scheduler/job dan publication control-plane S01 aktif. Coordinator menjalankan PARSE→STRUCTURE→BIND→CHUNK durable; worker Rust dan Semantic Gateway dapat menjalankan EXTRACT dengan kontrak/prompt/model terpin. Commit durable output EXTRACT oleh coordinator, API query, retrieval/answering, RESOLVE–INDEX, dan provider/model produksi belum tersambung. Lihat [panduan akuisisi](../../doc/acquisition.md).

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Collector/audit D01, kontrak/validator C01, evaluator E01, adapter control-plane S01, jalur durable sampai CHUNK, exact identity K01, worker EXTRACT, dan Semantic Gateway sudah tersedia. Commit EXTRACT oleh coordinator, resolusi semantik/merge-split, graph assembly, index/retrieval, mutasi backend, serta provider/model produksi belum aktif. Test correctness tidak membuktikan target kualitas atau latency.
