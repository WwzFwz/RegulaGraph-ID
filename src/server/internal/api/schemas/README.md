# src/server/internal/api/schemas

`comparison.go` membungkus bundle C01 per tanggal untuk HTTP/CLI dengan validasi scope, snapshot dan budget agregat. `reranking.go` berbagi serialization diagnostik bounded; tidak mengimplementasikan algoritma ranking. Format single-date tetap kompatibel. Lihat [kontrak](../../../../../doc/compare-evidence.md).

Kontrak transport HTTP bagi pertanyaan, dokumen, hasil job, dan response kesalahan. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menggantikan domain model atau membocorkan objek SDK database. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memetakan domain secara eksplisit, menentukan batas input, dan menyajikan citation serta status bukti tidak cukup dengan jelas. Perubahan bentuk response memperhatikan kompatibilitas klien.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Pertanyaan/jawaban saat ini memakai Protobuf JSON C01 langsung melalui
[routes/evidence.go](../routes/evidence.go) dan
[routes/questions.go](../routes/questions.go); kode error aman berada di handler
tersebut. Scaffold schema pertanyaan/error yang tidak dipakai telah dihapus,
bukan diganti kontrak kedua. Scaffold dokumen tetap disimpan untuk endpoint yang
belum aktif. Presence, enum invalid, tanggal, uint64, error redaction dan terminal
event tetap perlu diverifikasi saat transport diperluas; streaming belum aktif.

Berkas: [documents.go](documents.go).

## Benchmark dan perhatian performa

**API.** Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml); hasil belum diukur.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status folder: endpoint/schema dokumen belum aktif. Transport pertanyaan dan error sudah ditangani route di atas; streaming masih terbuka. Status sistem terkini mengikuti [rencana pengembangan](../../../../../doc/development-plan.md).

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [documents.go](documents.go) | Map document/version/job payloads without leaking SDK objects or storage paths. | Test historical versions, paging cursors and failed/partial job responses using contract fixtures. |
| [evidence.go](../routes/evidence.go) | Map domain/vendor failures to stable safe error codes and retry hints, preserving trace IDs. | Test malformed input, timeout, cancellation and conflict mapping; never serialize credentials or raw vendor errors. |
| [questions.go](../routes/questions.go) | Map public JSON to authoritative QuestionRequest/Answer/Event types with explicit optional/date/uint64 handling. | Round-trip null/absent fields and stream error/final cases against contracts; reject unsupported enums. |
