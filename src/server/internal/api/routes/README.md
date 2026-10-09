# src/server/internal/api/routes

`temporal.go` memvalidasi audit CURRENT/AS_OF dan header X-Effective-Date/X-Query-Time-Zone; tidak mengambil clock kedua. CURRENT hanya diterima bila zona operator tersedia. [Kontrak](../../../../../doc/current-query.md).

Definisi endpoint yang menerjemahkan HTTP ke pemanggilan workflow dan response schema. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak berisi implementasi pipeline atau pemanggilan vendor yang melewati dependency injection. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak konsisten memakai dependency, request ID, validasi input, dan penanganan kegagalan. Hindari pekerjaan CPU berat secara langsung pada event loop.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Liveness `/livez` dirakit di [server.go](../server.go); readiness `/readyz`
ditangani [evidence.go](evidence.go) dengan dependency runtime. Scaffold health
terpisah telah dihapus. Pertahankan timeout probe dan respons tanpa rahasia;
probe tidak menjalankan inference penuh.

`evidence.go` menyediakan `/v1/evidence` dan `/readyz` dengan bearer auth satu
corpus/profile, input C01, batas body, deadline dan fail-fast overload. Ia hanya
memanggil interface workflow dan tidak mengambil model/backend dari JSON caller.
`evidence_test.go` menguji unauthorized/malformed/overload/cancel dan error redaction.
`questions.go` menyediakan `/v1/questions` melalui handler yang sama, dengan
interface workflow dan capability eksplisit. Output C01 Answer/Evidence dibungkus
dengan mode draft/token usage; scope/snapshot/date/status dicek sebelum serialisasi.
`questions_test.go` menguji output salah, no fallback, shared overload dan cancel.
Route documents dan streaming tetap scaffold.

Berkas: [documents.go](documents.go), [questions.go](questions.go).

## Benchmark dan perhatian performa

**API.** Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml); hasil belum diukur.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status komponen: evidence HTTP empat profil dan complete draft answer opsional
aktif dengan autentikasi dan shared admission. Satu profile diotorisasi per
instance; request tidak dapat menggantinya. Documents, administrasi graph dan
streaming belum aktif. Unit/native integration PASS tidak membuktikan kualitas
hukum atau required benchmark.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [documents.go](documents.go) | Expose snapshot-bound document/version reads and ingestion job submission through workflow interfaces. | Test corpus isolation, unavailable historical versions, cursor mismatch and idempotent submissions. |
| [server.go](../server.go) | Report process liveness separately from backend/model/snapshot readiness without expensive full queries. | Test degraded dependencies and drain state; bound probe timeout and exclude secrets from responses. |
| [questions.go](questions.go) | Validate/authenticate question requests, call answer workflow once and stream typed events with backpressure. | Test client disconnect, exactly one terminal event, partial generation and unavailable snapshot; record TTFT and total latency. |
