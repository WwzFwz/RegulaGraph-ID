# src/ingestion

Crate Rust untuk pemrosesan dokumen serta transformasi knowledge graph dan indeks secara batch. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../AGENTS.md).

## Peran dan integrasi anak

Rust menyiapkan data dan artefak immutable, Go mengoordinasikan job serta commit/publikasi, dan C/C++ menyediakan engine parsing/inference. Belum ada transport job aktif.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [src/](src/README.md).

Berkas: [Cargo.toml](Cargo.toml).

## Benchmark dan perhatian performa

Ukur halaman/detik, mention/edge per detik, p95 waktu job, scaling core, peak RSS, dan freshness lag. Nilai sasaran wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml).

Ikuti [kebijakan benchmark](../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Boundary parser PDFium, normalizer teks konservatif, parser struktur hukum, structural chunk builder, parent index, proyeksi structure/chunk ke C01, dan penyimpanan artefak lokal immutable sudah aktif sebagai library. Parser memverifikasi hash input/library serta menghasilkan teks dan locator per halaman; transformasi berikutnya menjaga mapping byte raw-normalized, provision/source identity, token count, parent refs, dan hash artefak. Executable worker, `TextArtifact`/`DocumentBatch` lengkap, versioning, tabel, graph/index batch, OCR, gold dataset, object storage, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas, durability, atau latency produksi.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [build.rs](build.rs). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../doc/contracts-implementation.md).
