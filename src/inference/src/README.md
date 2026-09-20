# src/inference/src

Implementasi C++ bagi header inference publik. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

Tidak mengatur fusion atau citation; target saat ini hanya membuktikan layout build, bukan kecepatan inference.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [batching.cpp](batching.cpp), [cross_encoder.cpp](cross_encoder.cpp), [embeddings.cpp](embeddings.cpp), [runtime.cpp](runtime.cpp).

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [batching.cpp](batching.cpp) | Schedule length/token-aware bounded microbatches with deadlines, cancellation and separate query/bulk capacity. | Test overload, fairness, starvation and cancelled items; measure p95/p99 queue wait, utilization and RSS/VRAM. |
| [cross_encoder.cpp](cross_encoder.cpp) | Implement batched pair tokenization/scoring with stable pair IDs, calibrated score interpretation and explicit truncation. | Check Python/native score/rank parity including long legal clauses; measure reranking quality, batch wait and throughput. |
| [embeddings.cpp](embeddings.cpp) | Implement exact tokenization, pooling and normalization for the selected export; return item-correlated vectors and truncation metadata. | Check Python/native numeric and retrieval parity, dimensions/non-finite values, multilingual long inputs and queue-inclusive latency. |
| [runtime.cpp](runtime.cpp) | Own long-lived model sessions, tokenizer/config manifests, resource pools and explicit startup/shutdown; connect the C01 wire library during N01. | Measure cold start separately; test load failure cleanup, concurrent reuse and cancellation without per-request reload. |
