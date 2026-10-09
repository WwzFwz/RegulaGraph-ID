# Verifikasi provisional identity sampai graph terpublikasi

Dokumen ini mencatat bukti integrasi source-reviewed provisional identity,
durable RESOLVE LINK, worker Rust ASSEMBLE, publication dan retrieval. Ia bukan
evaluasi kualitas model atau keberlakuan hukum. Run memakai PostgreSQL, Qdrant,
Neo4j dan worker Rust nyata dengan corpus/schema/storage fixture terisolasi.

## Revisi, konfigurasi dan bukti

Baseline kode `167cb2b` ditambah perubahan fixture indexing; fingerprint file
teruji disimpan di `artifacts/verification/20261009-provisional-graph/manifest.json`.
Log pada direktori tersebut merupakan artefak lokal, tidak dipublikasikan sebagai
data corpus. Toolchain Go 1.26.8 windows/amd64, Rust/Cargo 1.87.0. Worker dibangun
offline dari source di `.cache/provisional-graph-build`, tanpa mengganti binary
worker lain yang sedang dipakai. Hash worker, ontology, PDFium dan tokenizer
startup dicatat pada manifest; ASSEMBLE tidak memanggil tokenizer atau PDF parser.

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| `cargo build --locked --offline --target-dir .cache/provisional-graph-build -p regulagraph-ingestion --bin regulagraph-worker` | Worker dari source terkini terbangun; `rust-build-isolated.log`, exit 0 | PASS build |
| `go test ./src/server/internal/indexing -run '^TestNativeProvisionalGraphPipeline$' -count=1 -v` | Dua identity source-reviewed menjadi dua endpoint graph yang berbeda; `native-provisional-final.log`, exit 0 | PASS integrasi |
| `go test ./src/server/internal/indexing` | Suite indexing tanpa opt-in backend lulus; `indexing-tests.log`, exit 0. Tes opt-in yang SKIP tidak diklaim berjalan | PASS unit/regresi yang aktif |
| `go vet ./src/server/internal/indexing` | Tidak ada temuan; `vet.log`, exit 0 | PASS |

Run awal gabungan di `native.log` meluluskan `TestNativeGraphAssemblyPipeline`
tetapi gagal pada assertion fixture provisional yang membandingkan profil tanpa
visibility dengan output ASSEMBLE. Rust memang menambahkan `Visibility.from_seq`
target. Tes diperbaiki untuk membentuk expected visibility dari **plan**, lalu
membandingkan seluruh field profil. Identitas/revision/status tidak diabaikan.
Run ulang provisional lulus; kegagalan awal tetap disimpan. `preflight.log`
sebelumnya berhenti di koneksi worker yang belum dinyalakan, bukan run sukses.

## Alur yang dibuktikan

1. Fixture EXTRACT memakai exact spans dari text artifact yang terdaftar.
   `SaveExtractionCheckpoint` menyimpan checkpoint dan katalog mention dengan
   owner/fence; request ingestion mem-pin candidate policy.
2. `InspectSourcedAlias` dan `Accept` membuat dua `defined_term` provisional
   melalui registry produksi. Dua scope lookup per mention tetap diperiksa;
   operator approval di sini adalah fixture eksplisit, bukan keputusan model.
3. `PlanRegistryCandidates` dan lookup registry menghasilkan kandidat pada
   revision terkini. `CommitAndCheckpoint` menyimpan durable LINK, intent,
   receipt dan checkpoint RESOLVE; row persetujuan reviewer disemai oleh tes.
4. Preparation/admission sumber memeriksa registry, source bytes, snapshot dan
   receipt sebelum satu RPC worker Rust. ASSEMBLE menghasilkan dua entity, dua
   mention, satu assertion dan satu support dengan canonical ID provisional.
   Profil registry seluruhnya sama setelah penambahan visibility target; status
   `UNREVIEWED` dan namespace `source-occurrence-provisional:v1` tetap utuh.
5. Processor baru memulihkan output committed dengan fence lebih baru tanpa RPC
   ulang. Byte artefak yang sama digunakan; preparation lama ditolak setelah
   checkpoint diganti. Tes juga menolak scope/ref/fence/checkpoint/pin yang salah.
6. Writer Neo4j dan receipt reuse Qdrant mempublikasikan snapshot gabungan.
   Typed reads mempertahankan bytes record; traversal menghasilkan satu path
   bersumber. GraphRAG memakai satu cabang, Hybrid GraphRAG memakai tiga cabang;
   hydration, reranking, context packing dan citation memproses output produksi.

Reviewer independen memeriksa diff, admission produksi, kegagalan awal, koreksi
visibility dan log final. Hasilnya **PASS terbatas integrasi fixture**; reviewer
tidak mengulang suite backend besar. Tidak ada temuan blocking tersisa.

## Reproduksi dan batas klaim

Aktifkan `REGULAGRAPH_TEST_NATIVE_GRAPH=1`, DSN PostgreSQL disposable pada
`REGULAGRAPH_TEST_POSTGRES_DSN`, `REGULAGRAPH_TEST_QDRANT_ENDPOINT`, endpoint
loopback `REGULAGRAPH_TEST_WORKER_ENDPOINT`, dan FileStore bersama worker pada
`REGULAGRAPH_TEST_GRAPH_ARTIFACT_ROOT`. Tambahkan `REGULAGRAPH_TEST_NEO4J_URI`
serta password lokal melalui environment untuk membuktikan publication/retrieval;
tanpanya subtest Neo4j SKIP. Varian test memasang mode provisional sendiri.
Jalankan worker dengan ontology dan dependency startup terpin sesuai
[panduan worker](../src/ingestion/src/bin/README.md). Gunakan backend tes, bukan corpus operasional.

BIND metadata, EXTRACT, initial vector, proposal/reviewer approval, generator dan
token counting merupakan fixture sintetis. Run ini tidak membuktikan model
EXTRACT/RESOLVE/GENERATE aktual, review UI/CLI, semantic CREATE otomatis, alias
tambahan ke target provisional, merge/split, legal-time inference, atau full PDF
end-to-end. CLI/API graph executable tidak diaktifkan dalam run ini. Pengujian
alias tambahan tetap pada [laporan terpisah](verification-report-alias-target.md).
Gold, false merge/split, kualitas retrieval/jawaban serta seluruh angka required
`configs/benchmark-targets.yaml` tetap **NOT_MEASURED**; durasi smoke bukan SLA.
