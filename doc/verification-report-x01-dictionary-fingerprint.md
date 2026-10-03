# Verifikasi fingerprint dictionary Go–Rust X01

Dokumen ini mencatat parity encoding antara mapping term-ID yang dibaca Go dari
PostgreSQL dan dictionary reader Rust. Dasar kode `d3e166f`, commit implementasi
`ce775c8`; fingerprint file
yang diuji: Go helper `07b7de8f989fc3539445455c509316355249bf2be67ff636a920ed59ad8ecdd6`,
Go test `21b5f0572e29a666a957b11ec2f649805d41936d6f0f28d68b9e36b1f63e8968`,
Rust reader/test `146b0adda3dc2ee6bb309aa83e17cb9c4610b71e29febd4377cb5771870c2968`.
Raw log ada di `artifacts/verification/20261003-x01-dictionary-fingerprint/`
(diabaikan Git). Lingkungan: Windows amd64, Go 1.26.8, Rust toolchain lokal
workspace, fixture dua term; tidak memakai model/corpus produksi.

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| `go test ./... -count=1` dari `src/server` | PASS, exit 0 (`go-test-all.txt`); fixture Go menguji nama revisi, hash byte v1, independensi urutan input, dan penolakan ID duplikat. |
| `cargo test --offline -p regulagraph-ingestion indexing::dictionary --lib` | PASS, exit 0 (`cargo-test-dictionary.txt`); 3 tes Rust termasuk hash fixture yang sama. |
| Hash fixture `analyzer=regulagraph-lexical-nfc-ascii-v1`, `revision=lexrev:2`, `izin=1`, `pasal=2` | Kedua bahasa menghasilkan `8199cf3ca12cb04c039ec1a296d025254b7a09ba1f675512d78c289fd80c4a7a`. |

Hash mapping sengaja tidak membawa corpus ID; pemanggil wajib mengikatnya pada
corpus, revisi registry, hash artefak, dan generation sebelum reuse/publication.
Artefak dictionary bertipe, pemeriksaan lineage lintas proses, corpus penuh,
latency/RSS hashing, dan gate kualitas/latency release tetap **NOT_MEASURED**.
Review independen read-only tidak menemukan blocker; ia menandai perbedaan
grammar ID Rust yang lebih longgar daripada producer SQL/Go, biaya sorting dan
salinan mapping pada 10 juta term, serta kebutuhan binding corpus terpisah.
Grammar producer dan byte-layout fingerprint dicatat pada rencana X01; analisis
peak RSS tetap pekerjaan acceptance.
