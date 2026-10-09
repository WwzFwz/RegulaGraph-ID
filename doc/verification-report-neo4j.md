# Verifikasi adapter graph Neo4j dan integrasi output Rust

Dokumen ini mencatat bukti penulisan generation graph awal, exact verification/seal
dan interoperabilitas output Rust pada 2026-10-09. Cakupannya mengikuti
[kontrak graph store](neo4j-graph-store.md); bukan completion Hybrid GraphRAG atau
publication graph PostgreSQL.

## Revision, lingkungan dan input

Base commit: `f35d6fc1e08fc4afbdffbc2b6702db413672dcc4`, ditambah perubahan adapter,
dependency driver dan tes integrasi pada milestone ini. Fingerprint file final,
perintah/exit code dan raw logs berada di
`artifacts/verification/20261009-neo4j/`. Direktori hasil lokal diabaikan Git.

Go 1.26.8 windows/amd64; Rust/Cargo 1.87.0; Neo4j Community 5.26.0 dan driver Go
v5.28.4; PostgreSQL 16.8-alpine; Qdrant 1.18.0. Image Neo4j:
`sha256:5a015e53de1895e7eee1574ae0325cf8c4b89587222778108c594bdd45a474b5`.
Worker Rust aktual memiliki SHA-256
`867e3a0bc33480ee08ed526a53e8aea97ad1af38576bd35a842b1e567fd9e314`.
Service tes memakai port loopback; tidak ada deployment aplikasi.

Adapter memakai fixture C01 sintetis. Pipeline native melanjutkan
[fixture native graph](verification-report-native-graph.md): BIND, EXTRACT, review
dan vektor awal masih sintetis; allocator/alias/candidate, intent/CAS RESOLVE,
preparation, inventory, RPC ASSEMBLE Rust dan commit STAGED PostgreSQL adalah kode
produksi. Ontology JSONC dan artefak worker memiliki hash yang diperiksa.

## Pemeriksaan dan hasil

| Pemeriksaan | Expected dan actual | Status/bukti |
| --- | --- | --- |
| Projection/configuration | Endpoint/type/visibility/base/revision salah ditolak; driver construction tidak mengakses jaringan. | PASS, `adapter-review-fixes.log` |
| Neo4j nyata | Schema replay, empat writer exact replay, dua sumber satu assertion, immutable conflict rollback dan stale fence ditolak. | PASS, `adapter-review-fixes.log` |
| Inventory/seal | Dua delta menghasilkan 5 record, 4 edge, 2 operation; incomplete inventory ditolak; urutan input tidak mengubah proof; new operation setelah seal ditolak. | PASS, `independent-fixed.log` |
| Corruption/budget | Label/property corrupt, extra/duplicate edge, incoming wrong-label edge ditolak; budget 256/64 MiB dan negative counter menolak operation baru, exact replay tetap sah. | PASS, `independent-fixed.log`; budget memakai fault injection counter, bukan load 64 MiB. |
| Output Rust aktual | Rust → PostgreSQL STAGED → Neo4j: 8 record, 5 edge, 1 operation; write replay dan exact seal. | PASS, `native-pipeline.log`, `independent-native.log` |
| Cold recovery | Processor baru memulihkan checkpoint tanpa RPC tambahan; historical checkpoints tetap ada. | PASS, `native-pipeline.log`, `independent-native.log` |
| Regresi Go | `go test ./src/server/...`; tes opt-in backend dieksekusi terpisah pada baris di atas. | PASS, `go-test.log` |
| Analisis statis Go | `go vet ./src/server/...`. | PASS, `go-vet.log` |

Perintah backend: `go test ./src/server/internal/adapters/neo4j -count=1 -v`, dengan
`REGULAGRAPH_TEST_NEO4J_URI` dan `REGULAGRAPH_TEST_NEO4J_PASSWORD` pada backend disposable.
Native memakai `go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`
dengan variabel opt-in pada header tes: native graph, root artefak bersama, worker,
PostgreSQL, Qdrant dan Neo4j. Seluruh perintah pada tabel exit 0.

## Review independen

Agent `verify_index_jobs` meninjau adapter dan menjalankan backend nyata secara
independen. Dua temuan telah diperbaiki: incoming edge dari node berlabel salah
dengan namespace sama sebelumnya dapat lolos, dan writer sebelumnya belum menahan
budget kumulatif generation sehingga inventory dapat menjadi tidak dapat di-seal.
Regresi keduanya kini lulus. Perbandingan properti juga dipindahkan ke database
agar readback tidak memindahkan payload corrupt tak berbatas ke klien.

Reviewer menyatakan PASS terbatas pada adapter setelah perbaikan, kemudian PASS
terpisah untuk helper/hook native dan rerun Rust/PostgreSQL/Neo4j. Raw evidence:
`independent-fixed.log`, `independent-results.json`, `independent-native.log` dan
`independent-native-results.json`. Tidak ada temuan blocking terbuka dalam scope ini.

## Batas hasil

Catalog/receipt graph PostgreSQL, publication gabungan dengan indeks, recovery
lintas backend, traversal query serta graph-grounded answers belum diuji/tersambung
oleh milestone ini. Closure incremental, takeover generation dan readiness cluster
belum tersedia. Model quality, gold corpus, correctness hukum dan required release
benchmark berstatus NOT_MEASURED. Tidak ada angka benchmark yang diubah.
