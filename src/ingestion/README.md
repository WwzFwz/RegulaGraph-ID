# src/ingestion

Crate Rust untuk pemrosesan dokumen serta transformasi knowledge graph dan indeks secara batch. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../AGENTS.md).

## Peran dan integrasi anak

Rust menyiapkan data dan artefak immutable, Go mengoordinasikan job serta commit/publikasi, dan C/C++ menyediakan engine parsing/inference. Transport gRPC batch tahap PARSE dan STRUCTURE menerima dispatch dari coordinator Go melalui job durable dan checkpoint fenced.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [src/](src/README.md), termasuk [worker](src/worker/README.md) dan executable [bin](src/bin/README.md).

Berkas: [Cargo.toml](Cargo.toml).

## Benchmark dan perhatian performa

Ukur halaman/detik, mention/edge per detik, p95 waktu job, scaling core, peak RSS, dan freshness lag. Nilai sasaran wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml).

Ikuti [kebijakan benchmark](../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Boundary parser PDFium, normalizer, structural chunking, proyeksi/persistence C01, incremental planner, dan selector timeline aktif sebagai library. Executable worker Tonic menjalankan PARSE dari PDF terverifikasi, mempertahankan source observation yang terikat blob, lalu STRUCTURE dari `DocumentBatch` immutable. STRUCTURE memverifikasi ulang raw/normalized/mapping beserta normalizer manifest, menghasilkan hierarki tanpa membuat ID ketentuan, dan mengembalikan batch baru yang terikat checkpoint. Binding provision/version sebelum CHUNK, extraction change-event, canonical registry, tabel, graph/index batch, OCR, gold temporal, object storage, full-rebuild equivalence, dan acceptance produksi belum aktif.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [build.rs](build.rs). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../doc/contracts-implementation.md).
