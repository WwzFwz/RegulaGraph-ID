# Verifikasi persiapan ASSEMBLE

Dokumen ini merekam pemeriksaan reader keputusan historis dan persistence plan/view
graph. Preparation menghasilkan locator artefak; laporan ini tidak membuktikan
dependency freshness, scheduling, worker lintas proses atau publication Neo4j.

## Revisi dan lingkungan

Reader receipt/port registry: `9e34c45`. Preparation dan regression: `b129fb8`.
Run 2026-10-09 memakai Go 1.26.8 windows/amd64, PostgreSQL 16.8 dan Qdrant 1.18.0
lokal disposable. Raw log ada di `artifacts/verification/20261009-graph-preparation/`.
Environment tes menggunakan `REGULAGRAPH_TEST_POSTGRES_DSN` dan
`REGULAGRAPH_TEST_QDRANT_ENDPOINT`; perintah di bawah dijalankan dari root.

Receipt nonempty memakai fixture RESOLVE LINK/DEFER, review, intent, decision ledger
dan FileStore nyata dari `TestSemanticRegistryAgainstPostgres`. Sumber/proposal/model
tetap sintetis. Fixture empty memakai jalur produksi CompleteEmptyResolution tanpa
registry operation. Preparation memakai published INDEX PostgreSQL/Qdrant dan source
receipt nyata dengan EXTRACT/RESOLVE kosong serta checkpoint yang disintesis eksplisit.
Ini bukan preparation nonempty penuh atau ingestion corpus/model nyata.

| Pemeriksaan/perintah | Expected dan actual | Bukti/exit |
| --- | --- | --- |
| `go test ./src/server/internal/adapters/postgres -run '^Test(EmptySemanticResolution\|SemanticRegistry)AgainstPostgres$' -count=1 -v` | Original RESOLVE direkonstruksi exact; changed intent/decision dan missing operation ditolak; PASS | `receipt-final.log`, 0 |
| `go test ./src/server/internal/workflows -run '^TestGraphAssembly' -count=1 -v` | Shared text dihitung sekali; missing/duplicate/MIME/budget ditolak; LINK diterima, DEFER/multiple assignment ditahan; PASS | `selection.log`, 0 |
| `go test ./src/server/internal/indexing -run '^TestPublishedGraphSourceMembershipAgainstStores$' -count=1 -v` | Plan/view tersimpan terverifikasi, replay exact, ontology drift ditolak; late cancellation tidak mengembalikan plan siap; PASS | `preparation.log`, lalu rerun final independen |
| `go test ./src/server/... -count=1` | Seluruh package Go lulus dengan integrasi backend aktif | `go-all.log`, 0 |
| `go vet ./src/server/...` | Tidak ada temuan pada kode final | `go-vet.log`, 0 |
| Reviewer independen `verify_index_jobs` | Review diff dan targeted backend tests; temuan unsupported action diperbaiki dan diverifikasi ulang; PASS | `independent.log`, `independent-selection.log`, `independent-preparation-final.log`, seluruhnya 0 |

Regresi menyeluruh dilakukan sebelum guard unsupported action ditambahkan. Selection
dan preparation backend diuji ulang setelah perubahan itu oleh implementer/reviewer.
Tidak ada perubahan proto, generated binding atau Rust/C++ pada paket ini; Rust builder
dibaca sebagai kontrak konsumen, tidak diklaim diuji lintas executable pada run ini.

## Temuan dan perbaikan

Reviewer menemukan bahwa receipt DEFER historis dapat diaudit, tetapi worker Rust
`materialize_resolved_relations` hanya bisa mengeksekusi LINK/CREATE dengan tepat satu
assignment. Preparation awal belum memisahkan keduanya. `ErrGraphAssemblyUnresolved`
kini menahan sumber sebelum exporter/write; mention tidak dihilangkan diam-diam.
Regression menggunakan fixture DEFER nonempty serta LINK/multiple assignment.
Review independen final tidak menyisakan blocker dalam cakupan preparation.

Pemeriksaan implementer juga menyesuaikan MIME normalized text dengan literal worker
dan mengikat ontology hash ke EXTRACT producer. Satu run `regression.log` gagal compile
karena test mengakses field protobuf oneof tanpa getter; `receipt-final.log` lulus
setelah fixture diperbaiki. Run gagal tidak dianggap PASS.

Review mencakup hash/registration dan bounded input, exact intent/ledger reconstruction,
canonical selection, revision/ontology/context, content-addressed storage, immutable
dependency registration, replay, serta final checkpoint/membership/publication rechecks.
Read gates tidak memegang lock lintas fungsi; admission job wajib membuktikan ulang.

## Sisa integrasi dan penerimaan

Record revision yang berbeda masih menghasilkan replan; belum ada reaffirmation durable
atau proof dependency freshness BIND/EXTRACT lintas revision. Source DEFER memerlukan
resolusi berikutnya; kemampuan membuat canonical baru/merge/split otomatis belum selesai.
Inventory/dispatch ASSEMBLE, commit output durable, writer Neo4j, graph retrieval dan
acceptance end-to-end tetap pekerjaan aktif. Lihat [kontrak](graph-assembly-inputs.md).

Kualitas model/legal, throughput, latency p95/p99, RSS dan benchmark release tetap
**REQUIRED_UNMEASURED** menurut [target wajib](../configs/benchmark-targets.yaml).
