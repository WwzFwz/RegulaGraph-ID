# src/inference/src

Implementasi C++ bagi header inference publik. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../AGENTS.md).

## Peran dan integrasi anak

Tidak mengatur fusion atau citation. `batching.cpp` sekarang menjadwalkan antrean query/bulk berbatas, deadline, cancellation, dan fairness lewat event loop satu thread. Kegagalan alokasi atau indeks mengakhiri proses worker; supervisor dan retry klien merupakan kewajiban integrasi runtime yang belum tersedia. ModelRuntime memiliki session/tokenizer; InferenceService mengatur admission dan pemetaan hasil C01; main membuka listener loopback setelah warmup.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [batching.cpp](batching.cpp), [batching_test.cpp](batching_test.cpp), [cross_encoder.cpp](cross_encoder.cpp), [embeddings.cpp](embeddings.cpp), [runtime.cpp](runtime.cpp).

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Runtime C01 embedding/reranking ONNX tersedia dengan session warm, manifest/hash verification, tokenizer native, batch query/bulk, cancellation, serta client Go/Rust. Acceptance kualitas dan performa pada workload referensi tetap NOT_MEASURED; lihat [panduan native](../../../doc/native-inference.md).

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [batching.cpp](batching.cpp) | Profilkan scheduler/session aktif di workload gabungan; lanjutkan supervisor/recovery dalam O01. | CTest memeriksa overload, fairness, deadline, serta cancel; p95/p99 queue wait, utilization, RSS/VRAM tetap NOT_MEASURED. |
| [cross_encoder.cpp](cross_encoder.cpp) | Pertahankan pair/token correlation dan raw logits; ukur downstream ranking serta long-input behavior pada gold. | Check Python/native score/rank parity including long legal clauses; measure reranking quality, batch wait and throughput. |
| [embeddings.cpp](embeddings.cpp) | Ukur retrieval parity untuk CLS/L2 export aktif, dengan token/provenance terpin dan tanpa truncation tersembunyi. | Check Python/native numeric and retrieval parity, dimensions/non-finite values, multilingual long inputs and queue-inclusive latency. |
| [runtime.cpp](runtime.cpp) | Profilkan lifecycle session/bundle/C01 aktif, cold startup, memory peaks, dan mixed workload. | Measure cold start separately; test load failure cleanup, concurrent reuse and cancellation without per-request reload. |

`service.cpp` menghubungkan scheduler dengan C01; `main.cpp` mem-pin bundle dan listener; `model_integrity.cpp` memindai sidecar; `token_probe.cpp` menghasilkan bukti token IDs memakai jalur serving.
