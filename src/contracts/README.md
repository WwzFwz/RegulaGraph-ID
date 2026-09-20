# src/contracts

Sumber definisi wire lintas Go, Rust, C++, serta evaluator Python. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../AGENTS.md).

## Peran dan integrasi anak

Kontrak menyatukan ID, versi, snapshot, error/status, dan satuan offset. Generated binding tidak menjadi definisi independen; service Worker kini memakai binding gRPC Go dan Rust dari proto yang sama.

Rancangan seluruh record serta operasi berada pada [system-contracts](../../doc/system-contracts.md); publication dan identity semantics berada pada [storage-consistency](../../doc/storage-consistency.md). File proto memiliki message/service descriptor C01 serta baseline kompatibilitas. Transport Worker loopback aktif untuk PARSE/STRUCTURE/CHUNK; kontrak stage-specific `ExtractionBatch` dan `ResolutionBatch` sudah tersedia, sedangkan executor keduanya, inference/registry, dan deployment TLS produksi belum aktif.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [jsonschema/](jsonschema/README.md), [proto/](proto/README.md).

Folder ini menyediakan artefak kontrak/verifikasi C01 sebagaimana daftar di bawah; tidak menyediakan layanan produksi.

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [CMakeLists.txt](CMakeLists.txt), [wire_validation.hpp](wire_validation.hpp), [wire_validation.cpp](wire_validation.cpp), [schema-lock.json](schema-lock.json). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../doc/contracts-implementation.md).
