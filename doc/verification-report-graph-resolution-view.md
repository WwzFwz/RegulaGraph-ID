# Verifikasi konteks RESOLVE sebelum graph preparation

Dokumen ini mencatat penyambungan dependency EXTRACT dan candidate-view reader
ke persiapan ASSEMBLE, bukan kelulusan seluruh K01 atau release Hybrid GraphRAG.
Kontrak input/output berada pada [input graph](graph-assembly-inputs.md).

## Kode dan fixture

Basis `225cd00`, ditambah implementasi `graph_resolution_view.go`, pemakaian callback
internal pada reader receipt, serta wiring preparation dan tes pada commit laporan
ini. Lingkungan 2026-10-09: Go 1.26.8 windows/amd64, PostgreSQL 16.8-alpine port
55448 dan Qdrant 1.18.0 port 56348, database/collection disposable.
Raw log: `artifacts/verification/20261009-graph-resolution-view/`.

Fixture semantic registry menjalankan commit LINK/DEFER, intent/review/ledger,
FileStore, checkpoint dan pembacaan ulang produksi; proposal/model sintetis.
Fixture tersebut kini mencantumkan dependency dokumen pada EXTRACT sebelum registrasi,
sesuai writer Rust. Fixture graph publication memakai PG/Qdrant nyata tetapi EXTRACT/
RESOLVE kosong sintetis. Tidak ada claim kualitas model/hukum atau native model run.

## Bukti

| Kasus | Expected dan actual |
| --- | --- |
| EXTRACT source-only | Dependency sumber exact diterima; hilang/duplicate/wrong hash/external artifact/positive-negative lookup tak dikenal ditolak |
| Nonempty RESOLVE | Reconstruction tetap identik setelah seluruh konteks kandidat diperiksa pada committed revision |
| Empty RESOLVE | Sumber tanpa mention diterima tanpa operasi registry atau kandidat fiktif |
| Penolakan reader | `ErrResolutionReplan` dari candidate reader diteruskan, tidak dikonversi menjadi hasil sukses |
| Revision mismatch | Target berbeda dari recorded revision ditolak; tidak ada penulisan ulang historis |
| Budget lookup | Dua scope dengan reference limit 100000 diterima tanpa mengurangi scope/candidate coverage |

Tes terarah `go test ./src/server/internal/workflows ./src/server/internal/adapters/postgres
./src/server/internal/indexing -run '^(TestGraphExtractionDependencies|TestSemanticRegistryAgainstPostgres|TestEmptySemanticResolutionAgainstPostgres|TestPublishedGraphSourceMembershipAgainstStores)$'
-count=1 -v` exit 0 (`targeted-fixed.log`). Seluruh `go test ./src/server/...`
exit 0 (`go-all.log`) sebelum koreksi budget. Sesudah koreksi, regresi
`go test ./src/server/internal/adapters/postgres -run '^TestSemanticRegistryAgainstPostgres$'
-count=1 -v` exit 0 (`budget-regression.log`); `go vet ./src/server/...` exit 0
(`go-vet.log`). Tes skip dengan prasyarat berbeda bukan PASS.

## Review dan batas hasil

Reviewer independen menemukan bahwa meneruskan reference limit sebagai alias cap
per scope menyebabkan reader menolak lookup kecil ketika produk scope × cap melampaui
aggregate limit. Implementasi diperbaiki: unique scope menentukan pembagian aggregate
budget, dan overflow tetap error tanpa truncation. Regression memakai konfigurasi
100000 references dengan positive/negative scopes nyata. Baseline review log ada di
`independent.log`. Verifikasi ulang reviewer `verify_index_jobs` pada kode final
exit 0 (`independent-final.log`), mencakup EXTRACT guards, empty/nonempty RESOLVE,
regresi 100000 references/multi-scope dan preparation PG/Qdrant. Temuan budget
ditutup; tidak ditemukan blocker baru dalam scope. Tes workflow setelah koreksi
juga exit 0 (`workflow-final.log`).

Pemeriksaan ini hanya read pada revision hasil commit; admission harus mengulang gate
dalam transaksi yang memegang authority. Reaffirmation lintas revision, inventory/
dispatch ASSEMBLE dan publication Neo4j belum selesai. Model accuracy, graph quality,
p95/p99/throughput/RSS pada workload required tetap **REQUIRED_UNMEASURED**. Target
[benchmark](../configs/benchmark-targets.yaml) tidak diubah.
