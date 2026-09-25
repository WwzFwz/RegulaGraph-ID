# Verifikasi renderer input embedding dan urutan term sparse X01

Dokumen ini mencatat pemeriksaan boundary library X01 pada Windows amd64,
2026-09-25. Raw log implementer berada di
`artifacts/verification/20260925-x01-render-input/` (diabaikan Git).
Fingerprint SHA-256 `src/ingestion/src/indexing/inputs.rs` yang direview
independen adalah `B8756E6D3BC36A29204E77400471B52396AFCD92FA708C62FE934B2FA36C3474`.
Toolchain lokal: Rust `rustc 1.87.0`, Go `go1.26.8`.

| Pemeriksaan | Expected dan hasil aktual | Status |
| --- | --- | --- |
| `cargo test -p regulagraph-ingestion --offline` | Renderer menjaga urutan label induk, rentang byte UTF-8, dan batas output; 151 unit + 1 PDF integration lulus, 2 ignored. Log `cargo-test.log`, exit 0. | PASS |
| Review independen `indexing::inputs::` dan probe adversarial | 4 tes renderer serta 2 probe pada salinan kode final lulus; span 8 MiB gagal sebelum alokasi, ancestor asing, urutan palsu, siklus, dan ID duplikat ditolak. | PASS pada boundary renderer |
| `go test -count=1 ./...` dari `src/server` | Batch lokal dan proyeksi Qdrant menolak indeks term sparse yang tidak naik ketat; seluruh paket Go lulus. Log `go-test.log`, exit 0. | PASS |
| `rustfmt --edition 2021 --check src/ingestion/src/indexing/inputs.rs`, `git diff --check` | Format dan whitespace sesuai. | PASS |
| Renderer melalui `ReadVerified`/worker INDEX, Qdrant hidup, publication, gold, benchmark required | Jalur proses dan prasyarat penerimaan ini belum dijalankan. | NOT_MEASURED |

Renderer menerima `DocumentBatch` dan normalized text yang **sudah** diautentikasi
oleh pemanggil. Ia memeriksa identitas artifact, batas UTF-8, struktur langsung,
rantai induk, dan batas 1 MiB sebelum membuat teks model. Hash output mengikat
byte hasil render dan versi policy; hash itu sendiri belum menjadi reuse key
model/generation. Pemanggil masih harus membuktikan membership snapshot serta
menjaga source/provision-version saat membentuk `IndexBatch`.

Gate sparse mencegah writer menerima vektor dengan ID term yang menurun atau
duplikat walau panjang dan nilai bobotnya valid. Ini adalah konsistensi input
lokal; tidak membuktikan parity BM25, kualitas retrieval, atau kesiapan Qdrant
nyata. Target dalam `configs/benchmark-targets.yaml` tetap
**REQUIRED_UNMEASURED**.
