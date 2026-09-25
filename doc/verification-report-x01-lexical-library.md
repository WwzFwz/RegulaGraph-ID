# Verifikasi library lexical X01

Dokumen ini mencatat hasil pemeriksaan analyzer Rust–Go dan pembobot BM25 berversi pada commit
`18312698fd4eeae4ca711c8c49f8970f3425dbc1`. Ini bukti subkomponen X01, bukan kelulusan
indeks produksi atau target benchmark. Raw output berada di
`artifacts/verification/20260925-x01-lexical-library/` (diabaikan Git).

## Cakupan dan hasil

| Pemeriksaan | Hasil | Bukti dan batas |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion indexing:: --offline` | PASS, exit 0, 12/12 | Analyzer, dictionary descendant, BM25 incremental/frozen dan sparse dot pada fixture kecil. |
| `go test ./internal/retrieval/query` | PASS, exit 0 | Analyzer query memakai fixture bersama, batas input dan regresi Hangul/Jamo. |
| `go run scripts/generate_lexical_letters.go --check` | PASS, exit 0 | 659 rentang Letter dan 1.375 rentang NFC Unicode 15 sesuai generator terpin. |
| Review agent independen | PASS untuk cakupan library | Menemukan dan menutup mismatch Unicode, panjang ID/lineage, serta kesalahan fixture cap; rerun final Rust 12/12. |
| Recall@k, nDCG, throughput, p95/p99, corpus penuh | NOT_MEASURED | Gold, backend Qdrant, dan alur query terhubung belum siap. Target `configs/benchmark-targets.yaml` tidak diubah. |

Toolchain: Go 1.26.8 windows/amd64 dan Rust 1.87.0. Fixture bersama
`tests/fixtures/lexical-analyzer-v1.json` SHA-256
`68F59968E1B1968FBDCE93D00F1B5CCDA37A0E3146CDF65EC950DAC40095A0AC`.
Go menulis peringatan telemetry karena token upload pada profil pengguna tidak dapat dibuat di
sandbox; perintah tes dan generator tetap exit 0 dengan hasil seperti tabel. Peringatan tersebut
bukan hasil benchmark atau kegagalan analyzer.

## Temuan terbuka dan handoff

Dictionary descendant membuktikan mapping lama tetap utuh secara lokal dengan digest dan
ancestor yang dimuat dari parent. Registry PostgreSQL masih harus memberikan bukti lineage
tepercaya, sementara pembaca snapshot harus memeriksa asal record terhadap dictionary yang
dipin. Dua sibling sama-sama kompatibel dengan statistics-base tidak otomatis kompatibel satu
sama lain sebagai record–reader. Artefak typed, allocator, IndexBatch, Qdrant writer/search,
publication/readiness, dan pengukuran kualitas/performa adalah pekerjaan X01 berikutnya.

Pemeriksaan ini mengikuti [protokol verifikasi](verification.md),
[kontrak](verification-contracts.md), [pipeline](verification-pipeline.md), dan
[kebijakan benchmark](benchmark-policy.md).
