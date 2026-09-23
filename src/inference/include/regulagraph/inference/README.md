# src/inference/include/regulagraph/inference

Antarmuka C++ untuk session, embedding, reranking, dan batching. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

`batching.hpp` kini mendefinisikan ownership metadata antrean, batas item/token, deadline, cancellation, dan prioritas query/bulk untuk satu event-loop thread. Header model lain masih scaffold; kelak definisikan ownership buffer, model identity, dan panjang input. Scheduler tidak memuat model saat header di-include.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [batching.hpp](batching.hpp), [cross_encoder.hpp](cross_encoder.hpp), [embeddings.hpp](embeddings.hpp), [runtime.hpp](runtime.hpp).

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [batching.hpp](batching.hpp) | Integrasikan antarmuka scheduler aktif dengan typed C01 errors, worker supervisor/retry, dan model warm. | CTest overload, fairness, deadline, cancellation lulus; p95/p99 queue wait, utilization, RSS/VRAM tetap NOT_MEASURED. |
| [cross_encoder.hpp](cross_encoder.hpp) | Define the cross_encoder interface with explicit ownership, lifetimes, typed errors and cancellation; keep implementation in matching .cpp. | Check Python/native score/rank parity including long legal clauses; measure reranking quality, batch wait and throughput; header inclusion must not allocate model resources. |
| [embeddings.hpp](embeddings.hpp) | Define the embeddings interface with explicit ownership, lifetimes, typed errors and cancellation; keep implementation in matching .cpp. | Check Python/native numeric and retrieval parity, dimensions/non-finite values, multilingual long inputs and queue-inclusive latency; header inclusion must not allocate model resources. |
| [runtime.hpp](runtime.hpp) | Define the runtime interface with explicit ownership, lifetimes, typed errors and cancellation; keep implementation in matching .cpp. | Measure cold start separately; test load failure cleanup, concurrent reuse and cancellation without per-request reload; header inclusion must not allocate model resources. |
