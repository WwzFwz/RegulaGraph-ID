# Verifikasi pengumpulan graph committed dan persiapan publication

Dokumen ini merekam verifikasi boundary PostgreSQL → source/GraphDelta validation
→ prepared write set pada 2026-10-09. Ia mendukung
[kontrak persiapan](graph-publication-preparation.md), bukan klaim publication atau
Hybrid GraphRAG selesai.

Base revision `39fdafd9a6b89636cf7b7e88fa36a32d24e40bd2`. Raw logs, perintah,
exit code dan fingerprint source berada di
`artifacts/verification/20261009-graph-completed/`. Toolchain: Go 1.26.8 windows/amd64,
Rust/Cargo 1.87.0; PostgreSQL 16.8-alpine, Qdrant 1.18.0, Neo4j Community 5.26.0.
Executable worker unchanged dari [run Neo4j](verification-report-neo4j.md).

| Pemeriksaan | Expected dan actual | Bukti |
| --- | --- | --- |
| PostgreSQL collection | Inventory lengkap, ref/plan owned; cancelled/unfinished child, source cancellation, source/child fence mismatch, foreign pin, cancellation dan stale registry stamp ditolak tanpa partial results. Pool satu koneksi berhasil. | PASS, `postgres.log`, `regressions.log` |
| Cancellation saat lock wait | Collector benar-benar menunggu lock corpus; cancellation child yang committed sebelum lock dilepas menghasilkan pending. | PASS, `regressions.log` |
| Rust fixture/source projection | Preparation valid; byte corrupt, hash-valid wrong assertion, producer/unknown fields, late authority loss dan checkpoint replacement ditolak. Dispatch existing memakai source reader bersama dan tetap lulus. | PASS, `workflow.log`, `regressions.log` |
| Multiple-source metadata | Lima assignment distinct diterima; shared physical output dan declared output gabungan di atas 64 MiB ditolak. | PASS, `regressions.log`; bukan load benchmark. |
| Native pipeline | Satu RPC Rust menghasilkan output committed; `PrepareCompletedGraph` memvalidasinya sebelum Neo4j menerima 8 record/5 edge/1 operation. | PASS, `native.log` |
| Recovery | Checkpoint baru tanpa RPC tambahan menolak prepared inventory lama; preparation baru berhasil, history dua checkpoint tetap ada. | PASS, `native.log` |
| Regresi/statis Go | `go test ./src/server/...` dan `go vet ./src/server/...`, exit 0; tes backend opt-in dieksekusi terpisah. | PASS, `go-test.log`, `go-vet.log` |

Agent `verify_index_jobs` memeriksa source/diff secara independen dan menjalankan
backend, workflow fixture Rust serta native pipeline. Tidak ada temuan blocking
dalam scope. Rerun tambahan memverifikasi race lock/cancellation dan metadata lima
sumber. Raw logs `independent.log`, `independent-final.log` dan fingerprint
`independent-results.json` membatasi PASS pada collection/preparation ini.

Perintah targeted memakai `go test ./src/server/internal/workflows ./src/server/internal/indexing`
dengan `-run '^(TestExecuteGraphAssemblyWithRustArtifacts|TestPublishedGraphSourceMembershipAgainstStores)$' -count=1 -v`.
Fixture Rust: `REGULAGRAPH_GRAPH_FIXTURE_DIR` menunjuk `20261009-graph-output/rich-fixture`.
Backend membutuhkan variabel tes PostgreSQL/Qdrant. Native memakai
`go test ./src/server/internal/indexing -run '^TestNativeGraphAssemblyPipeline$' -count=1 -v`
dan variabel worker/root shared/Neo4j pada header/panduan native. Semua run PASS pada
tabel exit 0.

EXTRACT/BIND/review/vektor awal adalah fixture sintetis. Tes metadata PostgreSQL memakai
delta locator sintetis; tes workflow membaca output Rust aktual; native menjalankan
RPC Rust dan backend nyata. Tidak ada live semantic-model call atau gold label hukum
yang dinilai. Catalog/intent/receipt graph, snapshot gabungan, traversal dan answering
masih diperlukan. Target benchmark tidak berubah; required quality/performance
berstatus NOT_MEASURED.
