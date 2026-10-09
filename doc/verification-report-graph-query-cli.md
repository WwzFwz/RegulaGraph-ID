# Verifikasi CLI graph query

Laporan ini mencatat konfigurasi graph query dan entrypoint operator setelah
revision `12b9d25`, 2026-10-09. Raw logs/fingerprints berada pada
`artifacts/verification/20261009-graph-query-cli/`. Toolchain Go 1.26.8 windows/amd64;
worker Rust, PostgreSQL, Qdrant dan Neo4j lokal nyata. Data/alias/model tetap sintetis.

`go test ./src/server/...` lulus (exit 0, `go-test.log`); config/CLI targeted tests
lulus pada `unit.log`. Native command
`go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`
dengan `REGULAGRAPH_TEST_QUERY_CLI` menuju binary yang dibangun dari source lulus
di `native-final.log`. Reviewer terpisah membangun ulang executable, menjalankan
config/CLI tests dan native integration; exit 0 pada `independent-build.log`,
`independent-unit-final.log` dan `independent-native.log`.

Expected: graph-only CLI tidak membutuhkan native embedding, memakai route
terotorisasi dan policy terpin, membuat pin sendiri, lalu mengembalikan evidence
graph bersumber. Actual: executable mengembalikan satu evidence dengan graph path,
snapshot tepat dan PARTIAL yang dipertahankan. Unit mencakup hash drift, corpus,
unknown/duplicate/case-variant JSON fields, UTF-8 invalid, illegal/plaintext remote
route, credential-in-URL, namespace, budget, conditional native dan secret redaction.

Native run awal gagal karena DSN subprocess mengarah ke default schema, sementara
fixture memakai schema terisolasi. Harness kini meneruskan DSN fixture yang tepat
dan menyimpan registered artifact bytes ke FileStore sebelum CLI. Diagnostic hook
sementara dihapus sebelum build final; error produksi tetap disanitasi.

Independent status dan fingerprint final berada pada `independent-results.json`.
Actual executable coverage adalah graph-only; hybrid-graph configuration/wiring
diuji unit, sedangkan workflow tiga cabangnya diuji native dengan model sintetis.
Belum ada real CLI + native embedding/reranker quality run. API graph wiring,
command generation dan gold/latency acceptance belum selesai. Target tidak berubah;
tidak ada klaim deployment atau release Hybrid GraphRAG lulus.
