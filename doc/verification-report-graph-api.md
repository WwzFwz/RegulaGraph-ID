# Verifikasi HTTP graph evidence

Laporan ini mencatat integrasi API graph setelah revision `857b0ce`, 2026-10-09.
Raw logs dan fingerprint file tersimpan di
`artifacts/verification/20261009-graph-api/`. Runtime: Go 1.26.8 windows/amd64,
worker Rust, PostgreSQL, Qdrant dan Neo4j lokal; data/alias/model fixture sintetis.

`go test ./src/server/...` dan `go vet ./src/server/...` lulus, exit 0
(`go-test.log`, `go-vet.log`). Native command
`go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`
dengan `REGULAGRAPH_TEST_NATIVE_GRAPH=1` dan `REGULAGRAPH_TEST_GRAPH_API=1`
lulus, exit 0 (`native.log`). Endpoint dan root artefak mengikuti konfigurasi
fixture; API menerima DSN schema terisolasi, bukan database default.

Expected: graph-only bekerja tanpa embedding service, cold queries bersamaan
dan warm queries menghasilkan bukti graph dari snapshot yang benar, readiness
memeriksa backend graph, dan jumlah lease kembali ke baseline fixture. Actual:
seluruh pemeriksaan tersebut lulus terhadap backend nyata. Route unit tests
menolak profil request yang berbeda dari profil server. Unit resource lifecycle
memeriksa pergantian snapshot dengan vector generation sama, borrower concurrent,
release idempotent, closed install dan penutupan setelah borrower terakhir.

Reviewer independen menjalankan unit suites dan native HTTP integration, exit 0;
lihat `independent-results.json` beserta log yang direferensikannya. Upaya race
detector **BLOCKED** sebelum tes oleh Cygwin gcc yang gagal membuat signal pipe
(Win32 error 5), dicatat pada `independent-race.log`; bukan race PASS.

Cache menyimpan resource generation, bukan hasil query atau lease. Runtime Close
harus dipanggil sesudah HTTP drain; tes cache bukan jaminan pemanggilan Close
arbitrer aman bersamaan dengan penggunaan shared PostgreSQL/FileStore. Coverage
HTTP native aktual adalah graph-only; hybrid-graph memakai unit wiring dan
library integration tiga cabang dengan model sintetis dari milestone sebelumnya.
Belum ada HTTP hybrid-graph dengan model produksi, generation jawaban operasional,
gold accuracy atau required latency/throughput acceptance. Target benchmark tidak
diubah dan tidak ada klaim release atau deployment selesai.
