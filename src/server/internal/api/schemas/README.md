# src/server/internal/api/schemas

Kontrak transport HTTP bagi pertanyaan, dokumen, hasil job, dan response kesalahan. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menggantikan domain model atau membocorkan objek SDK database. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memetakan domain secara eksplisit, menentukan batas input, dan menyajikan citation serta status bukti tidak cukup dengan jelas. Perubahan bentuk response memperhatikan kompatibilitas klien.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [documents.go](documents.go), [errors.go](errors.go), [questions.go](questions.go).

## Benchmark dan perhatian performa

**API.** Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml); hasil belum diukur.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../../../../../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [documents.go](documents.go) | Map document/version/job payloads without leaking SDK objects or storage paths. | Test historical versions, paging cursors and failed/partial job responses using contract fixtures. |
| [errors.go](errors.go) | Map domain/vendor failures to stable safe error codes and retry hints, preserving trace IDs. | Test malformed input, timeout, cancellation and conflict mapping; never serialize credentials or raw vendor errors. |
| [questions.go](questions.go) | Map public JSON to authoritative QuestionRequest/Answer/Event types with explicit optional/date/uint64 handling. | Round-trip null/absent fields and stream error/final cases against contracts; reject unsupported enums. |
