# Verifikasi batas graph, sparse query, dan teks jawaban

Dokumen ini mencatat pemeriksaan terhadap perubahan sejak revision dasar `d87dcda`:
`8d436a8` (helper K01 prapublikasi), `f8fd463` (encoder query BM25 Go), dan
`a48d622` (gate cakupan teks A01).
Ini bukti fungsi lokal, bukan kelulusan milestone end-to-end atau benchmark release.
Raw output ada di `artifacts/verification/20260925-k01-q01-a01/` (diabaikan Git).

## Input, proses, dan output yang diperiksa

| Pemeriksaan | Hasil | Ekspektasi dan batas aktual |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion --offline` | PASS, exit 0; 143 unit pass, 2 ignored, 1 integration pass | K01 menolak batch C01 tidak sah, LINK di luar kandidat/revision, endpoint hilang, self-loop canonical yang dilarang ontology, support kosong, dan ID bentrok. Test model native sungguhan dan wire roundtrip eksplisit tetap ignored. |
| `cargo test -p regulagraph-ingestion knowledge_graph:: --offline` | PASS, exit 0; 25/25 | Pemeriksaan terfokus graph setelah format. |
| `go test -count=1 ./...` dari `src/server` | PASS, exit 0 | Encoder BM25 memakai dictionary/statistics terpin lokal, bobot qtf×IDF, DF=0 untuk term descendant, dan menolak zero-value/DF kosong/mismatch. Gate A01 menolak teks di luar claim dan prosa bebas saat abstain/klarifikasi. |
| `rustfmt --edition 2021 --check` pada `builder.rs` dan `schema.rs`; `git diff --check` | PASS, exit 0 | Format file terdampak dan whitespace diff. `cargo fmt --all -- --check` belum hijau karena file tokenizer lama di luar diff masih tidak sesuai rustfmt. |
| Review agent terpisah | PASS dalam cakupan tiga fungsi lokal | Reviewer menemukan dan implementer menutup self-loop canonical, validasi penuh C01, celah claimless abstain, zero-value encoder dan DF kosong. Reproduksi tambahan reviewer PASS setelah perbaikan. |
| GraphDelta, Qdrant, answer generation, quality dan p95/p99 | NOT_MEASURED / belum terintegrasi | Tidak ada bukti publication, snapshot backend, entailment hukum, atau pencapaian target `configs/benchmark-targets.yaml`. |

Toolchain: Go 1.26.8 windows/amd64; Cargo/rustc 1.87.0. Fixture yang dijalankan
adalah unit/integration repo lokal, termasuk ontology `configs/ontology-v1.jsonc`
dan fixture lexical Unicode 15. Log: `rust-full.log`, `rust-graph.log`, dan
`go-all-uncached.log`. Pemeriksaan ini belum menjalankan workload corpus/gold.

## Temuan terbuka untuk alur penuh

K01 masih membawa ID assertion/support dari ekstraksi pada output prapublikasi.
Canonical assertion dedup, remap support, GraphDelta/closure, receipt committed registry,
writer Neo4j, dan verifikasi span terhadap byte sumber belum tersedia.
Encoder Go hanya menerima proyeksi artefak; hash artefak, ancestry dictionary dari
PostgreSQL, binding snapshot, serta sparse search Qdrant belum tersambung.
Gate jawaban hanya memeriksa cakupan struktur, bukan apakah klaim didukung secara
semantik; generator, tokenizer budget, graph proof, dan streaming masih diperlukan.
Tidak ada target required yang diturunkan atau dinyatakan PASS tanpa pengukuran.
