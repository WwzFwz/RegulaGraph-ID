# Verifikasi transport Qdrant X01

Dokumen ini mencatat audit adapter HTTP Qdrant terhadap revision dasar `df63e96`.
Raw output ada di `artifacts/verification/20260925-x01-qdrant-transport/`
(diabaikan Git). Ini bukti transport dan gate lokal; bukan bukti Qdrant hidup,
index publication, retrieval end-to-end, atau target benchmark.

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| `go test -count=1 ./...` dari `src/server` | PASS, exit 0; seluruh paket Go lulus termasuk tes HTTP Qdrant. Log `go-all-final.log`. |
| `go vet ./src/server/internal/adapters/qdrant`; `git diff --check` | PASS, exit 0; peringatan konversi line ending Windows saja. |
| `go test -race -count=1 ./src/server/internal/adapters/qdrant` | BLOCKED sebelum kompilasi kode karena Cygwin GCC gagal membuat signal pipe (`Win32 error 5`); log `go-race.log`. Tidak diklaim PASS. |
| Review agent independen | Mengidentifikasi respons kosong palsu, kesiapan tidak dicabut, redirect API key, validasi payload legal, budget memori, dan presisi sequence; perbaikan dan tes regresi lokal dijalankan ulang. |
| Qdrant nyata, failover replica, payload index, readback dan benchmark | NOT_MEASURED; engine Qdrant tidak tersedia pada run ini. |

Toolchain: Go 1.26.8 windows/amd64. Tes memakai server HTTP sintetis yang
memeriksa create/get collection, upsert dense+sparse dengan `wait=true` dan
`ordering=strong`, query dengan snapshot dan nested version filter, serta
menolak layout salah, hasil stale, respons malformed, payload hukum rusak,
redirect, dan urutan snapshot di luar rentang eksak. API didasarkan pada
[Qdrant v1.18 create collection](https://api.qdrant.tech/v-1-18-x/api-reference/collections/create-collection),
[upsert](https://api.qdrant.tech/v-1-18-x/api-reference/points/upsert-points),
[query](https://api.qdrant.tech/v-1-18-x/api-reference/search/query-points),
dan [nested filtering](https://qdrant.tech/documentation/search/filtering/).

Koleksi menggunakan satu generation fisik agar reader lama tidak menyamakan
format filter yang belum dikenal. Cakupan ini belum membuat payload index,
ledger operasi, closure/compensation, readback semua point dan replica,
resolver binding PostgreSQL, atau workflow retrieval. Ack upsert tidak boleh
dianggap bukti kesiapan snapshot. Caller masih wajib membuktikan sumber,
keanggotaan snapshot, serta fencing operasi sebelum upsert. Kualitas hukum,
Recall@k, p95/p99, dan target required tetap **NOT_MEASURED**.
