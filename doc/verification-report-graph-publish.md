# Verifikasi publication graph operasional

Laporan ini mencatat perubahan setelah revision `109b4cb`, 2026-10-09. Raw logs,
toolchain dan fingerprint berada pada `artifacts/verification/20261009-graph-publish/`.
Go 1.26.8 windows/amd64 digunakan dengan Rust worker aktual serta database fixture
PostgreSQL/Qdrant/Neo4j terisolasi. Source extraction dan review adalah data sintetis.

`go test ./src/server/...` dan `go vet ./src/server/...` lulus, exit 0
(`go-test.log`, `go-vet.log`). Affected unit suites memeriksa invocation/scope/hash,
deadline, malformed output, error redaction dan shared graph source restoration.
Publication coordinator mengomposisikan guard existing, bukan menyalin algoritma
projection, source authority, reuse, receipt atau active CAS.

Native command
`go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`
memakai `REGULAGRAPH_TEST_NATIVE_GRAPH=1` dan
`REGULAGRAPH_TEST_PUBLISH_GRAPH_CLI=<compiled CLI path>`. Test branch ini dimulai
sebelum manifest atau graph catalog/receipt dibuat oleh helper fixture. Actual
CLI melakukan cold source restoration, fresh stage, actual Neo4j write/readback,
Qdrant unchanged-index readback, kedua receipt dan aktivasi snapshot. Replay exact
menghasilkan snapshot sama; scope/generation berbeda ditolak. Parent historis,
graph traversal/hydration dan kedua profil retrieval tetap berfungsi. Exit 0 pada
`native.log`; test wall 10.165 detik bukan required latency benchmark.

`go test ./src/server/internal/indexing -run '^TestNativeGraphPublicationFailureRecovery$' -count=1 -v`
menginjeksikan kegagalan reused-index readback setelah graph write/receipt. Expected
dan actual: error tanpa snapshot sukses, parent tetap aktif, tidak ada Qdrant
receipt baru. Actual CLI kemudian mengulang publication identik sampai sukses,
tanpa worker/model call ulang pada tahap publication. Exit 0 (`recovery.log`,
test wall 8.921 detik). Semua pin operator sudah dilepas. Raw fixture juga menguji
recovery ASSEMBLE yang sudah ada secara terpisah sebelum publication.

Reviewer independen membangun CLI dan menjalankan kedua kasus. Hasil, perintah,
exit code dan fingerprint final tersedia di `independent-results.json` dan log
pendampingnya. Pemeriksaan akhir menambahkan guard protobuf manifest sebelum
dereference; regression native merusak payload manifest fixture setelah publication
dan menuntut replay gagal tanpa panic, lalu mengembalikan byte asli. Validasi ini
tidak mengubah format manifest atau angka benchmark.

PASS berlaku untuk orchestration/operator integrity yang diuji. CLI masih
memerlukan source receipts, preparation dan scheduling inventory ASSEMBLE melalui
library. Corpus end-to-end dengan extraction/model/gold nyata, canonical mutations,
full incremental closure, compensation/GC dan required quality/performance gates
belum selesai. Published replay membuktikan durable publication identity, bukan
health backend saat ini. Tidak ada deployment atau perubahan target benchmark.
