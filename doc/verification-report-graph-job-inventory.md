# Verifikasi inventory dan claim ASSEMBLE

Laporan ini mencatat pemeriksaan admission source/receipt/view dan transaksi inventory
graph, bukan kelulusan K01 keseluruhan. Implementasi: `8eeac3d`; koreksi registry:
`ee24b89`. Tanggal 2026-10-09, Go 1.26.8 windows/amd64, PostgreSQL 16.8-alpine,
Qdrant 1.18.0 lokal. Reviewer independen: agent `verify_index_jobs`.

## Bukti dan perintah

Raw logs berada di `artifacts/verification/20261009-graph-job-inventory/`.
Fixture sumber berada pada `src/server/internal/indexing/graph_jobs_test.go` dan
`graph_source_bindings_test.go` pada revision implementasi; EXTRACT/RESOLVE kosong
disintesis eksplisit. PostgreSQL/Qdrant, FileStore, source binding, registry reads,
inventory transaction dan claim memakai implementasi produksi.

| Pemeriksaan | Perintah / log | Hasil |
| --- | --- | --- |
| Regresi Go | `go test ./src/server/...`, `go-all.log` | PASS, exit 0; integration ber-env tidak dijalankan oleh perintah ini |
| Static checks | `go vet ./src/server/...`, `go-vet.log` | PASS, exit 0 |
| Registry + inventory backend | `go test ./src/server/internal/adapters/postgres ./src/server/internal/indexing -run '^(TestRegistrySnapshot\|TestPublishedGraphSourceMembershipAgainstStores)' -count=1 -v`, `rollback-scope.log` | PASS, exit 0 dengan env backend lokal |
| Registry lock race | `go test ./src/server/internal/indexing -run '^TestPublishedGraphSourceMembershipAgainstStores$' -count=1 -v`, `registry-lock-race.log` | PASS, exit 0 dengan env backend lokal |
| Independent domain checks | `independent-units.log` | PASS, exit 0; ownership/order/budget/selection/dependency |
| Independent backend rerun | `independent-integration.log`, `independent-nonalias.log`, `independent-lock-race.log` | PASS, exit 0; termasuk generic lookup nonalias tetap bekerja |

Env yang diperlukan: `REGULAGRAPH_TEST_POSTGRES_DSN` ke PostgreSQL disposable dan
`REGULAGRAPH_TEST_QDRANT_ENDPOINT` ke Qdrant disposable. Fixture indexing memakai
schema dan collection terisolasi. Regresi lock ditambahkan setelah regresi Go penuh;
rerun implementer dan reviewer mengeksekusi seluruh integration test terdampak.

## Expected dan actual

Child insert failure harus rollback inventory; jumlah row sesudah kegagalan nol.
Replay exact tidak menambah job. Mutasi pointer plan pemanggil tidak mengubah plan
admitted. Corrupt view, cancelled source dan stale registry stamp ditolak; preflight
ulang setelah perubahan registry tak terkait memungkinkan replay pada target yang sama.
Pool satu koneksi tidak deadlock. Claim generik tidak mengambil graph child; claim
ASSEMBLE mempertahankan lease eksklusif, cancellation dan kenaikan fence pada reclaim.
Mutation inventory ditolak trigger append-only. Seluruh expected tersebut tercapai.

Regresi contention mengunci corpus row, mengamati waiter nyata melalui `pg_locks`,
menaikkan revision lalu melepas lock. Schedule menolak stamp lama setelah memperoleh
lock. Bump langsung dalam kasus ini adalah fault injection; perubahan registry melalui
allocator produksi diuji terpisah pada fixture yang sama.

Reviewer menemukan writer lookup generik dapat membuat alias scope negatif tanpa
menaikkan registry stamp. Perbaikan menolak seluruh namespace `lookup:` pada API
generik, termasuk scope yang belum pernah ada. Regresi membuktikan tidak ada row baru,
sedangkan lookup nonalias tetap berfungsi. Temuan ditutup oleh rerun independen.

## Batas klaim

PASS hanya untuk inventory/admission/claim dan invariants yang diuji. Rangkaian
inventory source nonempty belum teruji penuh, meski reader/ledger/candidate gate
memakai komponen yang mempunyai tes terpisah. Dispatch Rust, verifikasi/commit output,
Neo4j, reaffirmation lintas revision dan end-to-end corpus belum selesai. Model/gold,
latency/throughput/RSS workload required tetap NOT_MEASURED; tidak ada perubahan target.
