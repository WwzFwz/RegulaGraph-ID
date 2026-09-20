# src/server/internal/ingestion

Koordinasi akuisisi sumber dan penjadwalan batch ingestion dalam Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Transformasi parsing, chunking, dan graph di src/ingestion; folder ini tidak menjalankan transformasi CPU berat. Sumber diteruskan sebagai locator/hash/job ID.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [sources/](sources/README.md).

Discovery, akuisisi, dan audit inventory D01 sudah aktif melalui CLI, workflow batch, dan adapter sources. Audit menghasilkan inventory deterministic untuk pemilihan sample M01/G01. Coordinator PARSE→STRUCTURE berada pada workflow Go dan memanggil transformasi Rust; canonical identity direncanakan oleh domain registry lalu dialokasikan adapter PostgreSQL, bukan oleh folder koordinasi ini. Stage EXTRACT–INDEX dan binding record regulasi masih scaffold.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Collector dan audit inventory D01 serta koordinasi PARSE→STRUCTURE sudah aktif; kontrak C01, evaluator E01, control-plane S01, transformasi dokumen Rust, dan exact identity K01 tersedia pada komponen pemiliknya. Graph extraction/index, binding record regulasi, dan layanan model belum aktif. Audit integrity tidak membuktikan kualitas ekstraksi atau benchmark produksi.
