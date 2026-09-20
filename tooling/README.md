# tooling

Tooling Python offline untuk eksperimen dan persiapan model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../AGENTS.md).

## Peran dan integrasi anak

Tidak diimpor serving Go atau worker Rust; hasil disalurkan sebagai artefak model terversi dan kontrak parity.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../src/contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [models/](models/README.md), [corpus/](corpus/README.md).

Berkas: [__init__.py](__init__.py).

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Collector/audit D01, kontrak C01, evaluator E01, serta control-plane S01 tersedia. Profiler corpus baseline M01 aktif di tooling/corpus; pemilihan parser/OCR/model native dan benchmark produksi belum selesai. Status anak dijelaskan pada header masing-masing; heuristic profile tidak membuktikan kualitas parsing atau target latency.
