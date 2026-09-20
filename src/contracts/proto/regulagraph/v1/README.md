# src/contracts/proto/regulagraph/v1

Lokasi kontrak wire versi awal untuk dokumen, job, graph delta, bukti, jawaban, serta inference. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

File sudah mendefinisikan message, enum, aturan field dan service descriptor C01. Katalog field semantik seluruh sistem telah dirancang pada [system-contracts](../../../../../doc/system-contracts.md), termasuk ownership, API/RPC/event, ID, presence, error, dan offset UTF-8 byte end-exclusive. Cakupan codegen, validator dan fixture yang tersedia dijelaskan pada [implementasi C01](../../../../../doc/contracts-implementation.md); konsumen produksi tetap memerlukan verifikasi integrasinya.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [answers.proto](answers.proto), [common.proto](common.proto), [documents.proto](documents.proto), [evidence.proto](evidence.proto), [graph.proto](graph.proto), [inference.proto](inference.proto), [jobs.proto](jobs.proto).

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Kontrak checkpoint mengikat terminal outcome worker agar recovery PARSE/STRUCTURE mempertahankan hasil sukses, parsial, dan pembatalan; nilai `UNSPECIFIED` tetap dipertahankan untuk membaca checkpoint lama secara fail-safe. Pipeline graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.
