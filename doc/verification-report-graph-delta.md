# Verifikasi GraphDelta upsert

Laporan ini mencatat assembly GraphDelta Rust yang terikat dokumen, teks normalisasi,
registry dan keputusan RESOLVE. Baseline `b1ac15c`, tanggal 2026-10-09. Fingerprint kode,
status percobaan dan raw log berada di `artifacts/verification/20261009-graph-delta/`.
Kontrak pemakaian dan batas kepercayaan ada di [graph-delta.md](graph-delta.md).

## Bukti aktual

| Pemeriksaan | Hasil / log |
| --- | --- |
| `cargo test -p regulagraph-ingestion --lib knowledge_graph::assembly --offline` | PASS 15/15, exit 0, `assembly-final.log` |
| Rerun independen perintah assembly yang sama | PASS 15/15, exit 0, `independent.log`; reviewer `/root/verify_index_jobs` |
| `cargo test -p regulagraph-ingestion --lib --offline` | PASS 174, failed 0, ignored 2, exit 0, `rust-lib.log` |
| `rustfmt --edition 2021 --check` pada file Rust terdampak | PASS exit 0 |
| Benchmark required/gold/Neo4j publication | NOT_MEASURED; belum dijalankan |

Toolchain rustc/cargo 1.87.0 Windows. Fixture memakai validator DocumentBatch/EXTRACT
produksi dengan keputusan LINK sintetis dan bytes teks yang hash-nya cocok. Ini bukan
model extraction nyata atau persetujuan hukum. Dua tes ignored memerlukan endpoint
embedding native serta fixture wire hasil generator; tidak dihitung sebagai PASS.

Expected/actual: endpoint/qualifier berpindah ke canonical ID; support dan source
refs tetap utuh; lookup negatif dan artifact/ontology dependencies dipertahankan;
visibility memakai target sequence. Reordering entities/proposals/decisions tidak
mengubah bytes delta. Source input tidak dimutasi. Tipe atau revision registry salah,
foreign corpus, missing identity, dependency conflict, self-dependency, overflow,
partial source, hash teks salah, span palsu, dan mention yang tidak cocok ditolak.
Tes Unicode memeriksa teks valid lebih dahulu, lalu offset di tengah code point
ditolak; ini tidak hanya mengandalkan kegagalan hash/ref fixture.

## Percobaan gagal dan review

`build.log` menyimpan compile failure awal karena mutasi melalui MessageField;
kode diperbaiki dengan akses mutable eksplisit. `assembly.log` dan `delta.log`
menyimpan fixture gagal: referensi teks versi belum disinkronkan dan storage key
mengandung karakter terlarang. Fixture diperbaiki mengikuti validator produksi;
validator tidak dilonggarkan. `delta-fixed.log` merupakan rerun empat tes delta
yang sukses, diikuti suite final dan independen. Exit code run gagal adalah 101,
bukan PASS. Cargo fmt pada sandbox awal tidak bisa memulai cargo metadata;
rustfmt/check dan compiler berhasil setelah akses toolchain disetujui.

Reviewer tidak menemukan blocker pada cakupan additive transform. Registry membership,
hash bytes artefak bertipe dan committed receipt tetap wajib diautentikasi caller.
Report valid membuktikan admission struktur/sumber yang diperiksa, bukan kebenaran
fakta, kecepatan produksi, atau publication readiness. Writer harus mempertahankan
visibility historis ketika entity/assertion dipakai bersama; target sequence output
tidak boleh dipakai untuk overwrite sejarah tanpa before-image/version yang sah.

Pekerjaan berikut: ekspor view registry terverifikasi, worker ASSEMBLE, coordinator
GraphDelta, Neo4j, closure incremental, aliases/profiles dan acceptance terintegrasi.
K01 penuh belum selesai; tidak ada perubahan target benchmark atau schema C01.
