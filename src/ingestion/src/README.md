# src/ingestion/src

Kode library Rust untuk domain lokal, dokumen, graph engineering, persiapan indeks, dan adapter native. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

lib.rs mengekspos module tree, proyeksi wire C01, adapter artefak lokal, dan service worker Tonic. Worker menerima batch PARSE, STRUCTURE, dan CHUNK; Go tetap memegang job durable, registry identity, dan publication.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [adapters/](adapters/README.md), [bin/](bin/README.md), [document/](document/README.md), [domain/](domain/README.md), [indexing/](indexing/README.md), [knowledge_graph/](knowledge_graph/README.md), dan [worker/](worker/README.md).

Berkas: [lib.rs](lib.rs).

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Modul parsing, normalisasi, structural chunking, validator, persistence batch, incremental planner, selector timeline, statistik BM25 lokal, blocking kandidat, dan helper alias dari LINK registry telah aktif. Worker gRPC menjalankan PARSE, STRUCTURE, CHUNK, dan EXTRACT terkonfigurasi dengan descriptor terverifikasi, fencing/idempotency, completion eksplisit, serta checkpoint terminal. EXTRACT membagi batch secara terbatas, memanggil Semantic Gateway berkorelasi, mengagregasi accounting/error item, memvalidasi proposal dengan ontology bytes terpin, dan menyimpan `ExtractionBatch` content-addressed. Coordinator Go mengklaim, memverifikasi, meng-commit, dan memulihkan output EXTRACT secara durable; provider/model produksi, stage RESOLVE, table/OCR, graph lanjutan, indexing pipeline, object storage, dan publication penuh belum aktif.
