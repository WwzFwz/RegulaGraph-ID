# Verifikasi K01: proposal resolusi berbasis kandidat registry

Dokumen ini mencatat verifikasi helper Rust yang merakit proposal LINK/DEFER dari pilihan
eksplisit dan `RegistryCandidateBatch` terpin. Ini bukan verifikasi keputusan registry,
stage RESOLVE, kebenaran legal identity, atau target performa produksi. Kode yang diuji:
commit `31125b563d07b68b3744dbad5d7fb3bd6689b99f`; SHA-256
`src/ingestion/src/knowledge_graph/resolution/resolver.rs` adalah
`79bb13b0e272a1218c9cbe0477a7e2294ea10d96ae80b27b3064642fbb601c51`.

## Hasil dan batas pembuktian

Run 2026-09-24 pada Windows amd64 dengan Cargo/Rust 1.87.0 dan schema C01. Fixture
memuat mention bersumber, homonym kandidat dalam satu scope, revisi lookup, dan hash
artefak EXTRACT. Tanpa pilihan eksplisit, satu kandidat pun tetap DEFER. LINK hanya
menerima canonical ID yang muncul pada lookup mention yang sama. Proposal membawa
evidence span/source, revisi registry, serta ID yang berubah bila hash sumber, isi
batch kandidat, revisi, atau pilihan berubah.

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion --lib --locked --offline` | 129 lulus, 0 gagal, 1 ignored; exit 0; `cargo-test-lib-final.log` | PASS untuk tes yang dijalankan |
| `cargo check -p regulagraph-ingestion --bins --locked --offline` | Binary worker terkompilasi; exit 0; `cargo-check-bins.log` | PASS |
| `cargo fmt --all --check` dan `git diff --check` | Format/whitespace valid; exit 0. Git hanya memberi peringatan konversi line ending Windows. | PASS |
| Review agent independen, read-only | Menemukan collision ID lintas revisi, snapshot/status salah, batas scope, serta scan alias kuadratik; perbaikan ditinjau ulang dan tidak ada blocker lokal tersisa. Reviewer tidak menjalankan tes sendiri. | PASS untuk review helper lokal |
| Receipt registry, byte artefak tepercaya, dan stage RESOLVE | Helper tidak membaca storage atau melakukan assignment; entrypoint worker dan Go coordinator belum terhubung. | BLOCKED |
| False merge/split, candidate recall, latency/throughput required | Gold resolution dan run pada workload referensi belum tersedia. | NOT_MEASURED |

Raw log berada di `artifacts/verification/k01-resolution-proposals-20260924/` dan
diabaikan Git. Tes regresi mencakup pilihan asing/duplikat, kandidat tanpa alias,
revisi stale, snapshot silang, status canonical ditolak, hash sumber tidak valid,
corpus konteks salah, serta banjir lookup scope. Pemeriksaan Rust ini menjaga kontrak
helper; Go tetap wajib memverifikasi artefak EXTRACT/kandidat dan receipt registry
sebelum keputusan atau publikasi. Target `configs/benchmark-targets.yaml` tidak
berubah dan tetap **REQUIRED_UNMEASURED**.
