# src/server/internal/api

Antarmuka HTTP Go HTTP untuk menerima request, mengikat dependency, dan memetakan hasil workflow menjadi response. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menjalankan logika inti extraction, retrieval, atau generation langsung di route. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memakai schema request/response eksplisit, error yang konsisten, dan request ID. Pekerjaan ingestion panjang direncanakan sebagai job; jangan menyamakan liveness dengan kesiapan semua dependency.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

`dependencies.go` membuka PostgreSQL/FileStore/native/HTTP clients eksplisit dan
memakai satu cache generation immutable; setiap request mempunyai snapshot lease
baru. `server.go` merakit route evidence, auth/concurrency/deadline, request ID
server dan log redacted. `server_test.go` memeriksa graceful drain; native HTTP
diuji melalui indexing/native_api_test.go. Lihat [panduan](../../../../doc/evidence-api.md).

Subfolder: [routes/](routes/README.md), [schemas/](schemas/README.md).

Berkas: [dependencies.go](dependencies.go), [server.go](server.go).

## Benchmark dan perhatian performa

**API.** Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); hasil belum diukur.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status komponen: evidence HTTP lokal vector/hybrid aktif, dengan autentikasi, bounded admission, fresh snapshot lease, shared clients, readiness dan graceful drain. Route generation jawaban, documents, graph dan streaming belum aktif. Unit/native integration PASS tidak membuktikan kualitas hukum atau required benchmark.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [dependencies.go](dependencies.go) | Construct and close shared pools/clients explicitly; inject narrow workflow interfaces and pin model/config manifests. | Test partial startup cleanup and dependency failure without opening connections during package initialization. |
| [server.go](server.go) | Wire HTTP routes, request size limits, deadlines, admission control and graceful drain; keep readiness separate from liveness. | Exercise slow clients, cancelled streams, overload and shutdown with in-flight requests; measure queue-inclusive latency. |
`preview.go` dan `preview.html` menyediakan UI localhost serta status, ask dan PDF
routes untuk workflow preview. Route membatasi body/deadline, Host/Origin dan
memverifikasi PDF terhadap receipt; teks model ditampilkan melalui textContent.
Tes boundary berada di `preview_test.go`. Cmd/api menyediakan evidence HTTP
terpisah; UI preview dirakit CLI demo, dijelaskan dalam
[panduan](../../../../doc/interview-demo.md).
