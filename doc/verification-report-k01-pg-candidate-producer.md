# Verifikasi K01: producer kandidat dari baca PostgreSQL

Dokumen ini mencatat adapter yang mengubah lookup alias PostgreSQL satu snapshot menjadi
`RegistryCandidateBatch` C01 untuk RESOLVE. Cakupannya adalah validasi plan, deduplikasi
scope, pemetaan hasil positif/negatif, serta pemeriksaan key, revisi, dan alias. Ia belum
memverifikasi transaksi database aktual atau menjalankan workflow RESOLVE. Kode diuji pada
parent commit `afce8f2`; SHA-256 `registry_candidate_batch.go` adalah
`f77bd7b532f29629786b8a6aaf3a8d94f7c17875066f654e80b6abe789df1e97`.

## Bukti dan status

Run 2026-09-24 memakai Go 1.26.8 windows/amd64. `PrepareRegistryCandidateBatch` menerima
scope hukum eksplisit per mention dan memanggil `LookupCanonicalAliases` sekali untuk
seluruh key unik. Reader PostgreSQL yang sudah ada menjalankan transaksi repeatable-read;
helper baru menolak respons dengan key/revisi salah, kandidat tanpa alias pendukung,
atau alias di luar scope. Nol mention menghasilkan `ErrNoCandidateMentions` tanpa pembacaan
database; workflow harus memperlakukan ini sebagai skip RESOLVE, bukan kegagalan job.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `go test ./...` dari `src/server` | Seluruh paket Go lulus; exit 0; raw `go-test.log`. Fixture adapter baru lulus, integration PostgreSQL memerlukan DSN yang tidak tersedia | PASS untuk tes yang dijalankan |
| `go vet ./...` dari `src/server` | Tidak ada temuan; exit 0; raw `go-vet.log` | PASS |
| `git diff --check` | Tidak ada kesalahan whitespace; exit 0, peringatan line ending Windows | PASS |
| Review agent independen | Memeriksa kontrak `LookupCanonicalAliases`, closure hasil, batas, dan zero-mention; temuan diperbaiki, tes adapter/domain diulang, tidak ada blocker lokal | PASS untuk boundary lokal |
| PostgreSQL transaksi nyata, receipt artefak EXTRACT, dan callsite workflow | `REGULAGRAPH_TEST_POSTGRES_DSN` tidak tersedia, workflow RESOLVE belum memanggil adapter | BLOCKED |
| Candidate recall, false merge/split, p95/p99 required | Gold dan workload referensi belum tersedia | NOT_MEASURED |

Raw log tersimpan di `artifacts/verification/k01-pg-candidate-producer-20260924/` dan
diabaikan Git. Scope/normalized lookup adalah input kebijakan RESOLVE, bukan tebakan
adapter. Validasi pre-read memeriksa meta/context/source ref/producer; workflow tetap
wajib membuktikan byte dan hash artefak EXTRACT sebelum memanggil adapter. Alias
`support_refs` dari registry belum divalidasi terhadap artefak sumber pada jalur ini.
Target numerik di `configs/benchmark-targets.yaml` tidak berubah dan tetap
**REQUIRED_UNMEASURED**.
