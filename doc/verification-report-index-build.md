# Verifikasi perakitan INDEX dan handoff Rust–Go

Laporan ini mencatat bukti paket worker INDEX pada 2026-10-04: plan immutable,
perakitan dense/BM25, source projection, admission Go dan wire lintas bahasa.
Status paket lokal **terverifikasi**; X01 keseluruhan, publication dan acceptance
RAG produksi **belum selesai**. Tidak ada perubahan target benchmark.

## Revision dan lingkungan

Baseline `ee87fdc`; kode hasil pada `2ec862f0d74740ff41a822c14fde9229c02f8a0f`:

- `7e352ae`: schema plan, generated binding dan fixture lintas bahasa.
- `19c328f`: assembler Rust, handler worker dan native bootstrap.
- `647b8d9`: admission Go terhadap source dan plan.
- `2ec862f`: pemeriksaan policy statistik pada reader query.

Windows amd64; Go 1.26.8, Rust 1.87.0, Python 3.12.4; C++ probe dibangun dengan
MSBuild 16.11.2/Visual Studio 2019 dan protobuf 34.1. Cargo target lokal
`.cache/cargo-target`; script interop memakai `.cache/rust-target`. Log dan bytes
fixture berada di `artifacts/verification/20261004-index-build/`; keluaran wire
tambahan berada di `.cache/contracts/interop`. Tidak ada credential dalam evidence.

## Pemeriksaan yang dijalankan

| Perintah / kasus | Expected vs actual | Exit/status |
| --- | --- | --- |
| `python scripts/generate_contracts.py` | Binding regenerasi 8 schema | 0 / PASS |
| `python scripts/check_contracts.py` | Aditif, baseline tidak ditulis ulang; 171 message, 32 enum, 4 service | 0 / PASS |
| `cargo check -p regulagraph-ingestion --bin regulagraph-worker` | Bootstrap optional native client terkompilasi | 0 / PASS |
| `cargo test -p regulagraph-ingestion --lib` | 161 lulus, 2 ignored; ignored bukan PASS native | 0 / PASS cakupan yang dijalankan |
| `go test ./src/server/...` | Semua package test non-opt-in lulus; external DB/backend opt-in tidak dibuktikan run ini | 0 / PASS cakupan yang dijalankan |
| `go vet ./src/server/...` | Tidak ada diagnostic | 0 / PASS |
| `cmake --build .cache/contracts-build --config Release --parallel 2` | Probe C++ dengan schema baru terbangun | 0 / PASS |
| `python tests/integration/wire_roundtrip.py` | 68 fixture sama melalui Go, Rust, C++, Python | 0 / PASS |
| `git diff --check` | Tidak ada whitespace error | 0 / PASS |

`REGULAGRAPH_INDEX_FIXTURE_DIR` disetel ke folder `interop` run ini pada tes Rust
dan Go. Tes CHUNK existing diperluas untuk memakai DocumentBatch/text artifact
nyata di filesystem, analyzer, dictionary, statistik dan plan yang hash-nya
diverifikasi. Embedding adalah fixture dua dimensi; handler memanggil port model
yang sama dengan adapter native. Expected: semua chunk terpilih menghasilkan
record dengan source filters yang sama, sparse nonkosong, counts lengkap, checksum
dan checkpoint INDEX. Actual sesuai. Retry input identik menghasilkan referensi
output identik; cancellation dan hasil dense parsial gagal tanpa respons sukses.

Input duplikat/hilang, snapshot/model/policy salah, hash statistik berubah, source
terlalu besar, artefak lexical terlalu besar dan total dictionary melewati budget
ditolak sebelum model dipanggil. Bytes output Rust diekspor lalu dibaca Go
`TestRustIndexBuildAdmission`; Go memeriksa hash source/plan, closure source,
metadata, dependency, target dan commitment vector. Sebelas mutasi adversarial
ditolak, termasuk filter palsu meskipun checksum dihitung ulang. Reader query
juga menolak statistik dengan policy kosong atau berbeda.

Log final: `rust-tests-final.log`, `go-tests-final.log`, `go-vet.log`,
`worker-build.log`, `wire-interop.log`; metadata perintah/exit code dan fingerprint
file tersimpan di `run.json`. Beberapa log awal merekam stderr PowerShell sebagai
NativeCommandError walaupun proses selesai exit 0; status di atas memakai exit code
proses dan hasil tes, bukan klasifikasi wrapper PowerShell.

## Review independen dan perbaikan

Reviewer `/root/verify_candidate_contract` memeriksa diff/binding dan menjalankan
sendiri pemeriksaan schema serta admission Go terhadap output Rust. Temuan:

1. Batas source Rust lebih besar daripada admission Go: disamakan ke 16 MiB /
   100.000 wire items sebelum render/native RPC. Corpus besar perlu partitioning
   dependency-complete, bukan pembuangan chunk.
2. Loader typed dapat membaca 64 MiB sebelum decoder 16 MiB menolak: diperbaiki
   menjadi preflight 16 MiB. Total analyzer/statistik/rantai dictionary dibatasi
   64 MiB agar satu selection tidak memicu pembacaan sekitar 1 GiB.
3. Loader query belum mengikat input policy statistik: sekarang policy wajib sama
   dengan generation. Fixture offline legacy tetap terpisah dari fixture serving.

Reviewer menyatakan tidak ada blocker lokal tersisa setelah pemeriksaan ulang.
Regression test terkait ditambah dan suite Rust/Go dijalankan kembali. Ini review
boundary lokal, bukan persetujuan publikasi ataupun kualitas jawaban hukum.

## Batas pembuktian dan kelanjutan

- **NOT_MEASURED:** INDEX penuh dengan BGE-M3 pada corpus nyata; tes baru memakai
  model fixture. Runtime native nyata memiliki bukti terpisah dari milestone N01.
- **NOT_MEASURED:** quality/gold, Recall/nDCG, throughput/RSS dan p95/p99 workload
  referensi. Gate required tetap berlaku sesuai `configs/benchmark-targets.yaml`.
- **Belum tersambung:** pembuatan/partisi plan dari registry otoritatif, dispatch
  coordinator durable, katalog generation, writer Qdrant, prior-state closure,
  ledger/recovery/readiness dan publication snapshot.
- **Belum tersambung:** concrete evidence hydration, generator tokenizer, reranker
  dan antarmuka untuk menjalankan PDF→jawaban RAG secara utuh.

Kelanjutan prioritas adalah handoff coordinator INDEX sampai writer/readback dan
publication, kemudian hydration/answering. Deployment tetap di luar scope pengguna.
