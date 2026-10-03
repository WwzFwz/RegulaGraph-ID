# Verifikasi artefak generation BM25

Dokumen ini merekam pemeriksaan kontrak dan handoff statistik lexical Rust ke
Go untuk X01/Q01. Perannya membuktikan binding artefak, kesamaan scoring serta
perilaku penolakan; bukan acceptance kualitas model atau klaim RAG siap produksi.
Semantik dan cara integrasi berada pada [lexical-generation](lexical-generation.md).

## Kode dan lingkungan

Tanggal 2026-10-03; baseline `5f0eb42`. Commit `241c1a9` menambah schema/generated
binding/fixture, `9010b2d` menambah producer-reader Rust, dan `2159f5c` memuat
validator serta factory Go. Baseline `schema-lock.json` tidak diubah.
Raw log dan `run-manifest.json` berisi fingerprint file/fixture berada di
`artifacts/verification/20261003-lexical-generation/` (diabaikan Git).

Lingkungan: Windows amd64, Go 1.26.8, Rust 1.87.0, Python 3.12.4, protoc 34.1,
C++ Visual Studio 2019/MSBuild 16.11.2 dengan protobuf 34.1.0. Qdrant disposable
1.18.0 menggunakan digest
`sha256:b3063c673f3973877c038eeecc392bad5011f072ee7892b56c9a8e204a3bdea9`,
loopback 56333 tanpa mount data pengguna. Collection unik dihapus oleh tes.

## Pemeriksaan yang dijalankan

Perintah dari root repo; GOCACHE `.cache/go-build` dan target Rust indexing
`.cache/cargo-target`. Runner wire memakai `.cache/rust-target` sendiri.

| Pemeriksaan | Perintah | Hasil / log |
| --- | --- | --- |
| Kompatibilitas schema | `python scripts/check_contracts.py` | PASS exit 0; 169 messages, 32 enums, 4 services, baseline tidak ditulis ulang; `schema-compatibility.log`. |
| Build C++ | `cmake --build .cache/contracts-build --config Release --parallel 2` | PASS exit 0; regenerated bindings dan wire probe; `cpp-build.log`. |
| Interop empat bahasa | `python tests/integration/wire_roundtrip.py` | PASS exit 0; 64 kasus Go/Rust/C++/Python termasuk presence dan unknown-field preservation pesan baru; `wire-roundtrip.log`. |
| Semua Go | `go test ./src/server/... -count=1` | PASS exit 0 untuk suite lokal; tes PostgreSQL/native/real Qdrant opt-in tanpa environment dilewati; `go-all.log`. |
| Analisis Go | `go vet ./src/server/internal/domain ./src/server/internal/retrieval/...` | PASS exit 0; `go-vet.log`. |
| Rust indexing | `cargo test -p regulagraph-ingestion --lib indexing:: --no-default-features` | PASS exit 0; 30 tes; `rust-final.log`. |
| Qdrant nyata | Set `REGULAGRAPH_TEST_QDRANT_ENDPOINT=http://127.0.0.1:56333`, lalu `go test ./src/server/internal/retrieval -run TestRetrieveBranchesAgainstQdrant -count=1 -v` | PASS exit 0; factory lexical memakai artefak terverifikasi, dense/BM25 menemukan record pada seq 7, snapshot lebih tua ditolak oleh guard statistik BM25; `qdrant-verified-lexical.log`. |

Fixture statistik memiliki tiga dokumen: satu kosong, satu berisi izin/pasal/pasal,
dan satu izin. N=3, total token=4, empty=1, DF izin=2/pasal=1. Builder Rust
menghasilkan bytes identik dengan fixture bersama. Query Go dan Rust memakai
bobot yang cocok; dot product sparse Rust cocok dengan skor referensi BM25.
Urutan input/token tidak mengubah fingerprint populasi; perubahan term mengubahnya.
Populasi fingerprint:
`aad68a3d328457ca032d8821250746ccad906186ee0ebf4994abe349bd03a212`.

Pembacaan FileStore nyata lulus untuk hash/size/type-bound artifacts. Tes negatif
menolak corruption, wrong corpus/ID/media type, budget habis, cancellation, formula
dan dictionary drift, DF impossible, serta term descendant yang disisipkan ke
statistik dictionary dasar. Reuse descendant terverifikasi menghasilkan DF=0 bagi
term baru. FileStore dan Qdrant memakai data sintetis, bukan corpus PDF pengguna.

## Review dan batas hasil

Reviewer independen `/root/verify_candidate_contract` membaca diff dan menjalankan
tes Go serta compatibility scanner. Dua temuan ditutup: konstruktor branch yang
masih menerima encoder tanpa binding artefak sekarang menolaknya, dan pemeriksaan
versi Unicode Rust kini aktif pada jalur runtime/release. Reviewer memeriksa ulang
kedua perbaikan dan menjalankan regresi LexicalFactory, tanpa blocker tersisa.
Sesudah review, helper builder analyzer serta perbandingan penuh SnapshotRef pada
sequence sama ditambahkan; tes Rust dan seluruh Go/vet dijalankan ulang implementer.
Keduanya tidak mengubah schema atau kebijakan penerimaan yang direview.

Paket kontrak/producer/reader lexical ini telah diimplementasikan dan diuji.
X01 keseluruhan tetap terbuka: IndexBuildPlan typed, membership/authority katalog,
perakitan IndexBatch, worker INDEX, ledger writer/closure/recovery, dan publication.
Snapshot yang lebih baru hanya boleh reuse setelah admission membuktikan ancestry
serta compatibility; nomor sequence saja bukan proof. Fingerprint populasi bukan
pengganti bukti sumber/keanggotaan snapshot.

Gold/model quality, Recall/nDCG, latency p95/p99, peak RSS vocabulary besar dan
throughput skala corpus tetap NOT_MEASURED. Durasi suite tidak ditafsirkan sebagai
benchmark produksi. Target `configs/benchmark-targets.yaml` tidak diubah. Race
detector tidak dijalankan ulang; keterbatasan compiler Windows pada laporan RAG
sebelumnya belum terselesaikan. Deployment tidak dilakukan.
