# Verifikasi fingerprint dictionary Go–Rust X01

Dokumen ini mencatat parity encoding antara mapping term-ID yang dibaca Go dari
PostgreSQL dan dictionary reader Rust. Dasar kode `d3e166f`, commit implementasi
`ce775c8`, dan refactor pembaca query `bf725cd`; fingerprint file
yang diuji: Go domain helper `f6ba753386e8ea2813eef3530911c9234f8b408124edf378d85eeb85b6ed403b`,
Go adapter test `c1e97c14436076f53f8ab28babd54eb09702b5f8414bc5b157b38caeaf`,
query reader `43b760db13087c1a9c9c3dde304df0db06bfbe2504eae9f74b58734ea5671a99`,
Rust reader/test `146b0adda3dc2ee6bb309aa83e17cb9c4610b71e29febd4377cb5771870c2968`.
Raw log ada di `artifacts/verification/20261003-x01-dictionary-fingerprint/`
(diabaikan Git). Lingkungan: Windows amd64, Go 1.26.8, Rust toolchain lokal
workspace, fixture dua term; tidak memakai model/corpus produksi.

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| `go test ./... -count=1` dari `src/server` | PASS, exit 0 (`go-test-all-final.txt`); fixture Go menguji nama revisi, hash byte v1, independensi urutan input, penolakan ID duplikat, dan query encoder yang memakai fungsi domain bersama. |
| `go vet ./internal/domain ./internal/retrieval/query ./internal/adapters/postgres` | PASS, exit 0 (`go-vet-focused.txt`); literal fixture lintas paket diperbaiki setelah review menemukan peringatan `composites`. |
| `cargo test --offline -p regulagraph-ingestion indexing::dictionary --lib` | PASS, exit 0 (`cargo-test-dictionary.txt`); 3 tes Rust termasuk hash fixture yang sama. |
| Hash fixture `analyzer=regulagraph-lexical-nfc-ascii-v1`, `revision=lexrev:2`, `izin=1`, `pasal=2` | Kedua bahasa menghasilkan `8199cf3ca12cb04c039ec1a296d025254b7a09ba1f675512d78c289fd80c4a7a`. |

Hash mapping sengaja tidak membawa corpus ID; pemanggil wajib mengikatnya pada
corpus, revisi registry, hash artefak, dan generation sebelum reuse/publication.
Artefak dictionary bertipe, pemeriksaan lineage lintas proses, corpus penuh,
latency/RSS hashing, dan gate kualitas/latency release tetap **NOT_MEASURED**.
Review independen read-only menemukan peringatan `go vet` dan tautan README
stale setelah fungsi dipindah ke domain; keduanya diperbaiki dan tes/vet diulang.
Reader Go kini memakai fungsi domain yang sama dengan adapter. Grammar SQL
producer lebih sempit daripada parser Rust/Go, tetapi output producer termasuk
dalam keduanya. Biaya sorting dan beberapa salinan mapping pada 10 juta term,
serta binding corpus terpisah, tetap perlu dibuktikan sebelum publication;
analisis peak RSS masih pekerjaan acceptance.
