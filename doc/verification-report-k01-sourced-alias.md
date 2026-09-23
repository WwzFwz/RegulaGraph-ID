# Verifikasi K01: alias dari keputusan registry LINK

Dokumen ini mencatat verifikasi helper Rust yang membentuk satu alias bersumber dari mention,
proposal LINK, keputusan LINK, dan canonical entity yang sudah berasal dari registry Go.
Helper tidak membuat keputusan identitas, memeriksa receipt registry, menjalankan stage RESOLVE,
atau menulis PostgreSQL. Fingerprint kode yang diuji: parent commit `2b14814`, SHA-256
`src/ingestion/src/knowledge_graph/resolution/aliases.rs` =
`fdbccef139655e603b78f2a06f862677aaf2bfa4c0c16bf040a7cece752040a2`.

## Hasil dan cakupan

Run 2026-09-24 memakai Rust/Cargo 1.87.0, Windows amd64, schema C01 terpin, dan fixture
wire-valid dengan organisasi, mention, sumber, bukti byte span, proposal serta keputusan
LINK. Helper mengikat ID proposal/mention/canonical, revisi, tipe/scope, dan sumber; menghasilkan
alias deterministik dengan mention ID sebagai support. Tipe dibatasi pada `regulation` dan
`organization` yang saat ini diterima adapter PostgreSQL. Normalized lookup dibatasi 512 byte
tanpa NUL sesuai pemeriksaan Go. Buktinya tetap terbatas pada invariant fixture, bukan gold
resolution atau registrasi nyata.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion --lib --locked --offline` | 123 lulus, 0 gagal, 1 ignored; exit 0; `cargo-test.log` | PASS untuk tes yang dijalankan |
| `cargo test -p regulagraph-ingestion --lib --locked --offline knowledge_graph::resolution` | Setelah perubahan terakhir, 7 lulus, 0 gagal; exit 0; `cargo-resolution-final.log` | PASS |
| `cargo check -p regulagraph-ingestion --bins --locked --offline` | Binary worker terkompilasi; exit 0; `cargo-check.log` | PASS |
| `cargo fmt --all --check` dan `git diff --check` | Format dan whitespace valid; exit 0, Git memberi peringatan konversi line ending Windows | PASS |
| Review agent independen | Mismatch tipe/status, kardinalitas LINK, enclosing span/source evidence, dan batas lookup terhadap Go ditemukan lalu diperbaiki; reviewer memeriksa ulang helper lokal | PASS untuk review boundary lokal |
| Receipt/state registry dan penyimpanan PostgreSQL | Caller/RESOLVE stage belum terhubung; belum ada uji transaksi alias end-to-end | BLOCKED |
| False merge/split, pairwise F1, latency/throughput required | Gold resolution dan workload produksi belum tersedia | NOT_MEASURED |

Raw log berada di `artifacts/verification/k01-sourced-alias-20260924/` dan diabaikan Git.
Tes meliputi homonym scope, assignment tidak cocok, revisi stale, bukti hilang/salah teks,
span bukti yang mencakup mention, ID sumber yang salah, tipe tidak didukung, serta lookup terlalu panjang/NUL.
Keputusan LINK tetap harus diautentikasi terhadap receipt/state registry pada snapshot/revisi
yang sama sebelum alias diterbitkan. `provision` memerlukan perluasan registry storage dan
uji hukumnya sendiri; jangan menyelundupkan tipe tersebut lewat helper ini. Target numerik
di `configs/benchmark-targets.yaml` tidak diubah dan tetap **REQUIRED_UNMEASURED**.
