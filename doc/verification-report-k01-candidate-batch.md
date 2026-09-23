# Verifikasi K01: pembentukan dan closure kandidat registry

Dokumen ini mencatat boundary Go yang mengikat hasil lookup registry ke `ExtractionBatch`
dan menolak alias di luar scope kandidat yang diamati. Cakupannya adalah determinisme,
batas resource, identitas wire, serta revisi positif/negatif. Ia belum menjalankan stage
RESOLVE atau membuktikan kebenaran keputusan hukum. Perbaikan alias-to-scope berada pada
commit `ababe7c`; builder diuji pada parent revision tersebut dengan SHA-256
`candidate_batch.go` = `8f369c568b04c36744f5fd5448bd48a7a6d3b5826d0100066d20449ae1eb58a9`
dan `candidate_validation.go` =
`ac390a4fac156c9f1ac79085b834d228ef8d2dcffd248784c5208ad304c06e39`.

## Hasil

Run 2026-09-24 memakai Go 1.26.8 windows/amd64 dan fixture sintetis kandidat registry.
`AssembleRegistryCandidateBatch` mengurutkan lookup, ID kandidat, identity keys, alias support,
serta revisi scope pada salinan input; manifest dependency selalu mengikat artefak EXTRACT
dan semua lookup termasuk hasil kosong. Ukuran referensi dan wire byte dipantau sebelum
cloning. Output lulus validator closure dan C01 wire. Validator menolak alias yang
tidak berasal dari tuple lookup `(entity_type, scope, normalized_lookup, canonical_id)`
serta revisi lookup yang lebih baru daripada revisi batch.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `go test ./...` dari `src/server` | Semua paket Go lulus; exit 0; `go-test.log` | PASS |
| `go vet ./...` dari `src/server` | Tidak ada temuan; exit 0; `go-vet.log` | PASS |
| `git diff --check` | Tidak ada kesalahan whitespace; exit 0, peringatan line ending Windows | PASS |
| Review agent independen | Binding alias dan builder dicek terhadap proto, validator, serta pembaca PostgreSQL; temuan tentang urutan kandidat dan batas byte diperbaiki, tes terkait diulang, tidak ada blocker tersisa | PASS untuk boundary lokal |
| Read-only snapshot PostgreSQL, provenance alias `support_refs`, dan receipt artefak EXTRACT | Builder menerima observasi dari caller; belum ada wiring transaksional atau uji PostgreSQL untuk artefak lengkap | BLOCKED |
| Candidate recall/reduction, false merge/split, p95/p99 dan target required | Gold resolution dan workload referensi belum tersedia | NOT_MEASURED |

Raw log berada di `artifacts/verification/k01-candidate-batch-20260924/`; pemeriksaan
alias-to-scope sebelumnya di `artifacts/verification/k01-candidate-alias-binding-20260924/`.
Fixture builder memvalidasi output wire, tetapi fixture `ExtractionBatch` input adalah
subset sintetis untuk closure; ini tidak menggantikan verifikasi handoff EXTRACT nyata.
Langkah berikutnya ialah mengambil seluruh scope dari satu read-only transaksi registry,
memastikan source/alias dari baris tepercaya, menerbitkan artefak immutable, lalu
mendispatch RESOLVE dengan revision dan dependency yang sama. Target numerik dalam
`configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**.
