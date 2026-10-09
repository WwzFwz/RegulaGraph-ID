# Verifikasi catalog graph dan durable write intent

Laporan ini mencatat pemeriksaan implementasi catalog immutable dan writer graph pada 2026-10-09, berbasis revision `7eeed4bcedbc39e6db6d9f11df76035e9d39cbe0` ditambah perubahan yang fingerprint-nya disimpan di `artifacts/verification/20261009-graph-catalog/`. PASS hanya berlaku pada cakupan yang diuji.

| Pemeriksaan | Hasil aktual |
| --- | --- |
| Describe tanpa koneksi, duplicate/conflicting shared records dan binding clone | PASS |
| Batas catalog, URI, counts, refs dan identity | PASS |
| Gagal insert intent membatalkan catalog dalam PostgreSQL | PASS |
| Rust RPC → committed output → catalog/intent → Neo4j | PASS; 8 record, 5 edge, 1 operation |
| Write committed dengan acknowledgement hilang, lalu exact retry | PASS; intent tetap planned dan retry menghasilkan proof identik |
| Proof backend salah, perubahan route dan mutation catalog | PASS; ditolak |
| Activation sebelum readiness receipt | PASS; ditolak dan active snapshot tetap parent |
| `go test ./src/server/...` | PASS, exit 0; tes opt-in yang tidak diberi environment tetap dapat skip |
| `go vet ./src/server/...` setelah koreksi fixture | PASS, exit 0 |

Run memakai Go 1.26.8 windows/amd64, PostgreSQL dan Qdrant lokal disposable serta Neo4j 5.26.0-community. Worker Rust aktual SHA256 `867e3a0bc33480ee08ed526a53e8aea97ad1af38576bd35a842b1e567fd9e314` berjalan dengan artifact root run ini. Model extraction/review dan vector awal masih fixture sintetis; tidak ada quality acceptance dari run tersebut.

Perintah utama: `go test ./src/server/internal/domain ./src/server/internal/indexing -run '^(TestGraphCatalogBoundaries|TestNativeGraphAssemblyPipeline)$' -count=1 -v`; adapter juga diuji melalui `TestDescribeGraphWithoutNetwork`, `TestDriverConfigurationIsLazyAndBounded` dan `TestNeo4jGraphAgainstStore`. Environment backend/worker mengikuti fixture native graph. Raw log implementer berada di `native.log`, `recovery.log`, `go-test.log` dan `go-vet.log`.

Reviewer terpisah `verify_index_jobs` membaca diff dan menjalankan backend/native serta regression terakhir secara independen: PASS, exit 0, tanpa temuan blocking. Bukti berada di `independent.log`, `independent-final.log` dan `independent-results.json` dengan fingerprint kode.

Run awal `native-first.log` gagal pada cleanup fixture: AbortPublication menolak intent yang belum dikompensasi, sesuai kontrak. Cleanup tes kini menghapus hanya generation fixture, memverifikasi kosong, lalu mencatat compensation; ini bukan implementasi production GC/compensation. Go vet pertama menemukan literal test tak bernama setelah Binding menjadi alias lintas package; fixture diubah memakai field bernama dan vet diulang.

Receipt durable, activation graph, carry-forward index, graph query, takeover dan closure incremental belum termasuk milestone ini. Migration diuji pada schema disposable baru; rehearsal upgrade produksi belum diukur. Benchmark latency/throughput/RSS dan kualitas corpus/gold tetap REQUIRED_UNMEASURED. Target tidak diubah.
