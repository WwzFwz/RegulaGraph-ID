# Verifikasi commit output ASSEMBLE

Dokumen ini mencatat bukti transaksi output graph serta recovery acknowledgement.
Scope adalah library commit, bukan completion K01, worker daemon atau release.

## Identitas run

Tanggal 2026-10-09; baseline `de71c0a237a51cf153c1a417e2b0620ba23a3753`.
Commit implementasi: `f80aad5` (shared transaction helpers), `d6cb543`
(expiry fixture fix), `c09aca0` (atomic storage), `464130a` (workflow recovery). Raw logs berada pada
`artifacts/verification/20261009-graph-output-commit/`. Go 1.26.8 windows/amd64;
backend lokal PostgreSQL 16.8-alpine dan Qdrant 1.18.0 memakai schema/collection
fixture terisolasi. Tidak ada migrasi baru atau perubahan wire/benchmark.

## Input, proses dan output

Workflow menerima hanya `VerifiedGraphOutput` hasil pembacaan dan projection
admission. Port metadata PostgreSQL internal mempercayai pemeriksaan bytes tersebut;
bukan endpoint publik untuk delta tak tepercaya. Request, response dan dependencies
ke port merupakan salinan agar mutation tidak mengubah hasil admission.

Commit mengikat immutable inventory, live claim, source checkpoint, cancellation,
publication/base, registry stamp dan reader lease. Lock snapshot/corpus/source/child/
lease diambil sebelum pemeriksaan authority transaksional; artefak/dependency,
checkpoint dan STAGED disimpan bersama. Authority berbasis waktu diperiksa ulang
sebelum transisi akhir. Pool satu koneksi tetap dapat menyelesaikan transaksi.

Lost acknowledgement ditangani dengan read exact checkpoint pada STAGED child,
bukan write retry. Read memakai context terpisah berbatas dua detik. Blob worker
mendahului transaksi; orphan blob saat rollback tidak berarti graph dipublikasikan.

## Pemeriksaan

| Perintah/cakupan | Expected dan hasil | Raw log |
| --- | --- | --- |
| `go test ./src/server/internal/indexing -run TestPublishedGraphSourceMembershipAgainstStores -count=1 -v`, env backend lokal | PASS exit 0: rollback metadata/checkpoint/status; child/source cancellation; late source cancellation saat lock wait; registry movement saat lock wait; successful STAGED; exact acknowledgement reconciliation; different checkpoint ditolak | `integration-final.log` |
| `go test ./src/server/internal/workflows -run TestExecuteGraphAssemblyWithRustArtifacts -count=1 -v`, env rich Rust fixture | PASS exit 0: commit arguments preserved; port mutation isolated; successful/failed/lost acknowledgement; bounded reconciliation after cancellation; no write retry | `workflow.log` |
| `go test ./src/server/internal/adapters/postgres -count=1`, env PostgreSQL lokal | PASS exit 0: shared checkpoint/artifact transaction helper regressions dengan backend nyata | `postgres.log` |
| `go test ./src/server/...`, env rich Rust fixture, backend env tidak disetel | PASS exit 0: Go suite; backend integration yang memerlukan env terpisah skip | `go-all.log` |
| `go vet ./src/server/...` | PASS exit 0 | `go-vet.log` |

Fixture graph metadata pada integration test disintesis eksplisit; tidak membuktikan
bahwa Rust worker berjalan bersama PostgreSQL. Workflow tests memakai artefak rich
aktual Rust dari run sebelumnya, tetapi storage/RPC ports sintetis. Kedua jenis bukti
tidak digabung menjadi klaim end-to-end nyata.

## Review independen

Reviewer `/root/verify_index_jobs` membaca perubahan storage/workflow dan menjalankan
ulang pengujian. Hasil akhir **scoped PASS** tanpa blocker terbuka pada boundary
ini. `independent-shared.log` exit 0 mencakup StorageFoundation serta INDEX output
commit/checkpoint regression. Workflow dan lima recovery scenario lulus dalam
`independent.log`, tetapi file log itu secara keseluruhan exit 1 karena kegagalan
fixture indexing saat run awal; jangan menyebut seluruh log tersebut PASS. Run awal menangkap
edit fixture di antara penambahan race dan pemindahan DROP trigger; error trigger
tercatat pada `independent.log`. Rerun menemukan expiry fault injection dapat
melanggar CHECK `expires_at > created_at` pada fixture cepat. Perbaikan memundurkan
created_at sementara bersama expired timestamp dan mengembalikan keduanya sesudah
assertion. Tidak ada gate produksi yang dilonggarkan. `independent-fixed.log` lulus
exit 0 setelah perbaikan; log gagal tetap dipertahankan.

## Batas dan langkah berikutnya

Daemon ASSEMBLE, processor restart/reclaim yang membaca ulang output, run actual RPC
Rust bersama PostgreSQL dengan sumber nonempty, registry reaffirmation lintas revision,
Neo4j write/readiness/publication dan retrieval graph tetap belum selesai. Recovery
acknowledgement ini hanya membuktikan pemanggilan commit yang hasilnya tidak diterima.
Model quality, legal correctness, corpus penuh serta required latency/throughput
berstatus NOT_MEASURED; angka configs/benchmark-targets.yaml tetap berlaku.
