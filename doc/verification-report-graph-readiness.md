# Verifikasi receipt graph dan authority aktivasi

Laporan ini mencatat pemeriksaan 2026-10-09 atas perubahan setelah `15ee227`, dengan fingerprint kode dan raw log pada `artifacts/verification/20261009-graph-readiness/`. Cakupan adalah receipt Neo4j durable, recovery dan final graph authority guard; bukan acceptance seluruh GraphRAG.

Pipeline memakai worker Rust aktual, FileStore, PostgreSQL dan Neo4j nyata pada fixture nonempty yang menghasilkan 8 record, 5 edge dan 1 operation. Input extraction/review/vector awal sintetis. Toolchain dan worker sama dengan [run catalog](verification-report-graph-catalog.md).

| Pemeriksaan | Expected / actual |
| --- | --- |
| Gagal insert authority setelah applied/receipt | Seluruh transaksi rollback; intent tetap planned, receipt tidak ada |
| Lost acknowledgement receipt commit | Caller menerima error; replay menghasilkan receipt yang sama dan satu authority |
| Child cancellation atau registry berubah | Receipt baru ditolak |
| Final guard: cancellation source/child, registry/hash drift, authority hilang | Activation ditolak |
| Job sedang dikunci worker | NOWAIT menolak activation tanpa menunggu siklus lock |
| Authority valid | Mencapai trigger rollback pada UPDATE publication; active pointer tidak berubah |
| Checkpoint/output baru vs catalog lama | Ditolak walaupun current inventory dan catalog masing-masing valid |

Perintah native: `go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`, memakai environment backend/worker disposable yang sama dengan run catalog. Log `native.log` menguji receipt/recovery; `activation.log` menguji final guard; `output-swap.log` menambahkan regression output berbeda. `go-test.log` dan `go-vet.log` menyimpan pemeriksaan seluruh Go server; status akhir dicatat di results.json setelah proses selesai.

Hasil akhir seluruh pemeriksaan tabel: PASS. Native regression, `go test ./src/server/...`
dan `go vet ./src/server/...` selesai exit 0. Reviewer terpisah `verify_index_jobs`
mengulang native regression sesudah perbaikan serta `TestStorageFoundationAgainstPostgres`:
PASS, exit 0. Raw log `independent-fixed.log`, `independent-foundation.log` dan
fingerprint `independent-results.json` mencatat cakupan dan temuan yang ditutup.

Review menemukan binding output pada receipt belum dibandingkan langsung terhadap completed inventory. Perbaikan menambahkan kesamaan ordered refs sebelum transaksi. Test fault injection mengganti current checkpoint/ref dengan metadata valid lalu meminta receipt catalog lama; harus ditolak. Dua percobaan awal fixture melanggar uniqueness storage key/content sebelum mencapai boundary; log `regression.log` dan `final-regression.log` dipertahankan, fixture diperbaiki memakai identity, key dan digest berbeda.

Tes final CAS sengaja memasang receipt Qdrant sintetis untuk melewati gate backend sebelumnya dan trigger yang membatalkan UPDATE published. Ini membuktikan guard graph pada jalur CAS nyata, tidak membuktikan carry-forward indeks atau sukses aktivasi Hybrid GraphRAG. Benchmark required, kualitas model/gold, traversal dan produksi upgrade rehearsal tetap NOT_MEASURED. Deployment di luar scope pengguna.
