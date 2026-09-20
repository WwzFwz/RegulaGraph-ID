# src/ingestion/src

Kode library Rust untuk domain lokal, dokumen, graph engineering, persiapan indeks, dan adapter native. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

lib.rs mengekspos module tree, proyeksi wire C01, dan adapter artefak lokal; executable worker belum tersedia sampai kontrak transport diimplementasikan.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [adapters/](adapters/README.md), [document/](document/README.md), [domain/](domain/README.md), [indexing/](indexing/README.md), [knowledge_graph/](knowledge_graph/README.md).

Berkas: [lib.rs](lib.rs).

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Modul `document::parsing::pdf`, `document::normalization::text`, `document::chunking::structural`, `document::chunking::builder`, `document::chunking::parents`, validator `domain::chunks`, proyeksi `domain::document_wire`, dan `adapters::storage` telah aktif. Modul Rust lain masih scaffold sampai versioning, table/OCR handling, knowledge graph, indexing, object storage, dan worker batch diimplementasikan. Build serta fixture unit tidak membuktikan target kualitas corpus, durability storage, atau latency produksi.
