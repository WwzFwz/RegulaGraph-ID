# src/server/internal/api

Antarmuka HTTP Go HTTP untuk menerima request, mengikat dependency, dan memetakan hasil workflow menjadi response. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menjalankan logika inti extraction, retrieval, atau generation langsung di route. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memakai schema request/response eksplisit, error yang konsisten, dan request ID. Pekerjaan ingestion panjang direncanakan sebagai job; jangan menyamakan liveness dengan kesiapan semua dependency.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [routes/](routes/README.md), [schemas/](schemas/README.md).

Berkas: [dependencies.go](dependencies.go), [server.go](server.go).

## Benchmark dan perhatian performa

**API.** Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); hasil belum diukur.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [dependencies.go](dependencies.go) | Construct and close shared pools/clients explicitly; inject narrow workflow interfaces and pin model/config manifests. | Test partial startup cleanup and dependency failure without opening connections during package initialization. |
| [server.go](server.go) | Wire HTTP routes, request size limits, deadlines, admission control and graceful drain; keep readiness separate from liveness. | Exercise slow clients, cancelled streams, overload and shutdown with in-flight requests; measure queue-inclusive latency. |
