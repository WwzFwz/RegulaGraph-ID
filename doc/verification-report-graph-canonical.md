# Verifikasi identitas graph prapublikasi

Laporan ini mencatat library canonical assertion/support setelah revision `90b1628`
pada 2026-10-09. Raw logs dan fingerprint berada di
`artifacts/verification/20261009-graph-canonical/results.json`; kontrak pemakaian
terdapat pada [graph-canonical-identity.md](graph-canonical-identity.md).

## Hasil

| Pemeriksaan | Expected / actual | Status |
| --- | --- | --- |
| Identitas deterministik | Renaming extraction ID dan reordering set menghasilkan record canonical sama | PASS |
| Distinction dan provenance | Arah, predicate, condition, origin, ontology, temporal scope berbeda tidak tergabung; support sumber independen tetap terpisah | PASS |
| Exception closure | Exception di-remap sebelum hash parent; missing/cyclic refs gagal eksplisit | PASS |
| Budget | Input/item/output dibatasi, termasuk pertumbuhan ID hasil | PASS |
| Komposisi | EXTRACT/RESOLVE fixture melalui endpoint materializer lalu canonicalization; canonical set hilang ditolak | PASS |
| Regression reviewer | Unknown fields nested/root, normalized COMPARE invalid, collision dengan mention retained ditolak | PASS |
| Suite Rust library | `cargo test -p regulagraph-ingestion --lib --offline`: 170 passed, 0 failed, 2 ignored | PASS untuk tes yang dijalankan |
| Reviewer independen | `cargo test -p regulagraph-ingestion --lib knowledge_graph::assembly --offline`: 11/11, exit 0 | PASS |
| GraphDelta/Neo4j/runtime | Belum disambungkan pada paket ini | NOT_MEASURED |
| Gold/performance required | Tidak ada workload acceptance atau gold | NOT_MEASURED |

Toolchain: rustc/cargo 1.87.0. Fixture synthetic protobuf C01; tidak ada PDF/model
nyata pada tes library ini. Dua tes ignored membutuhkan native endpoint/model dan
wire-roundtrip fixture generator, sehingga tidak dihitung sebagai PASS pada run ini.
Tidak ada wire schema/binding atau target benchmark yang diubah.

Log `rust-lib.log` memuat broad suite; `assembly-reviewed.log` memuat regresi utama;
`independent-elevated.log` memuat rerun verifier setelah seluruh perbaikan kode.
Windows sandbox awal menolak build script/rustc dengan Access denied. Eksekusi
compiler lokal memakai escalation yang disetujui kemudian lulus; percobaan sandbox
independen tetap tercatat pada `independent.log`, bukan dianggap PASS.

## Temuan yang ditutup

1. Dedup tanggal COMPARE dapat membuat jumlah tanggal tidak valid. Record hasil
   normalisasi kini divalidasi ulang C01 sebelum diterima; regresi dua tanggal identik.
2. Unknown protobuf fields memakai iterasi HashMap dan tidak aman untuk hash canonical.
   Library menolaknya secara rekursif, termasuk source/resolution wrapper; transport
   C01 secara umum tetap mempertahankan unknown fields untuk compatibility.
3. Hash hasil dapat berbenturan dengan ID mention yang dipertahankan. Keunikan output
   lintas tipe diperiksa; komposisi juga memeriksa ID canonical entity yang diketahui.

Seluruh temuan telah diperiksa ulang reviewer. Identitas mempertahankan full temporal
scope secara konservatif dan belum menyimpulkan equivalence lintas knowledge snapshot.
Tes penarikan satu support hanya recomputation input library: belum withdrawal durable,
incremental/full-rebuild equivalence, validasi keseluruhan GraphDelta atau publication.
Unknown schema/cyclic exceptions tidak dipotong untuk membuat hasil terlihat lengkap.

Langkah lanjut: binding registry view historis, GraphDelta/dependency/closure, ASSEMBLE
worker dan coordinator, writer/readiness Neo4j, lalu query graph. Kualitas hukum/model
dan required throughput/latency/RSS tetap membutuhkan acceptance tersendiri.
