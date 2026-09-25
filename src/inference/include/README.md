# src/inference/include

Header publik C++ untuk kontrak wrapper inference. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

Header tidak memuat model pada static initialization; definisi wire tetap di src/contracts.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [regulagraph/](regulagraph/README.md).

Antarmuka session/model, scheduler, layanan C01, serta scanner dependensi ONNX sudah aktif; include header tidak memuat model.

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Runtime C01 embedding/reranking ONNX tersedia dengan session warm, manifest/hash verification, tokenizer native, batch query/bulk, cancellation, serta client Go/Rust. Acceptance kualitas dan performa pada workload referensi tetap NOT_MEASURED; lihat [panduan native](../../../doc/native-inference.md).
