# Verifikasi retry item semantic setelah cancellation

Dokumen ini mencatat perbaikan klasifikasi kegagalan model yang menghambat retry
EXTRACT/RESOLVE. Ia membuktikan recovery cache pada gateway yang tetap hidup,
bukan checkpoint durable atau keberhasilan extraction corpus.

## Pemicu dan perubahan

Adapter lokal dapat mengembalikan `context.Canceled` atau
`context.DeadlineExceeded` secara langsung, misalnya dari guard model sebelum
HTTP. Sebelumnya `operationError` memperlakukannya sebagai INTERNAL terminal.
Cache item menyimpan error tersebut; retry operation yang sama mendapat error
lama tanpa memanggil model lagi. EXTRACT juga menyimpan opaque error dari adapter
setelah context RPC berakhir, sedangkan RESOLVE sudah memeriksa context dahulu.

Helper bersama kini mengenali cause context dengan `errors.Is`, memberi code
CANCELLED/DEADLINE_EXCEEDED dan `retryable=true`, serta mempertahankan item ID.
EXTRACT memeriksa context RPC sebelum memetakan/menyimpan provider error. Error
validasi deterministik tetap terminal; tidak ada retry otomatis atau penghapusan
fakta. Caller tetap mengendalikan retry dan deadline.

## Bukti

Base revision `f8aba0758314f1808f4c49067d5af6f03e5701d0` ditambah perubahan
`semantic.go` dan `semantic_cancellation_test.go`. Fingerprint final/reviewer ada
di `artifacts/verification/20261009-semantic-retry/independent-results.json`.
Toolchain Go 1.26.8 windows/amd64; semua fixture memakai sumber sintetis.

| Pemeriksaan | Expected / actual | Bukti |
| --- | --- | --- |
| Tes baru melalui Go overlay kode sebelum perbaikan | Expected FAIL; actual exit 1: error INTERNAL terminal dan replay teracuni | `before-overlay.json`, `before-failure.log`; source lama hanya disalin ke artefak, bukan mengganti worktree |
| Tes cancellation langsung/wrapped, deadline, parent cancel dengan opaque error | PASS, exit 0; item sukses dipanggil sekali, item terputus dicoba kembali | `focused.log` |
| RESOLVE deadline saat context parent masih aktif | PASS; response error transient kemudian proposal pada retry | `focused.log` |
| Regresi inference, gateway, workflows | PASS, exit 0 | `regressions.log` |
| Review independen dan full inference suite | PASS_SCOPED | `independent-results.json`, log reviewer; reproduce red/green |

Item sukses pertama tetap memiliki satu panggilan provider walaupun item kedua
mengalami error transient, cancellation, lalu retry sukses. ProviderError yang
membawa context cause mempertahankan tipe cancellation/deadline. Sebaliknya error
`context_window` tetap non-retryable karena input perlu diubah sebelum dicoba lagi.

## Batas dan langkah berikutnya

Gateway restart masih menghilangkan cache. Rust masih memproses seluruh CHUNK
DocumentBatch, dan coordinator belum menyimpan hasil tiap item model saat batch
belum selesai. Penyelesaian durable membutuhkan identitas checkpoint yang mengikat
corpus/scope, source bytes/provenance, operation/item, model/prompt/schema/ontology,
dan producer config aktual; request ID/deadline baru tidak boleh mengubah identitas
pekerjaan semantik yang sama. Crash setelah persist sebelum response, stale writer,
output corrupt, config drift, dan resumption dengan subset lengkap harus diuji
sebelum klaim recovery lintas restart. Publication tetap menunggu seluruh input.

Kualitas model, full-PDF extraction, serta benchmark latency/throughput tetap
belum lulus. Perubahan ini tidak mengubah angka target atau klasifikasi hasil
eksperimen model sebelumnya.
