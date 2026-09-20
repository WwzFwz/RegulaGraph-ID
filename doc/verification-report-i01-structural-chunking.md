# Verifikasi struktur hukum dan chunking I01

Dokumen ini mencatat bukti implementasi parser struktur hukum, structural chunk builder, validator
record, dan parent index Rust pada 2026-09-20. Perannya mengikat perilaku, regression test, hardening,
dan audit agent independen ke revision yang dapat ditinjau. Verdict **PASS** hanya berlaku untuk scope
library deterministik; gold corpus, tokenizer produksi, worker/wire, throughput, RSS, dan acceptance
produksi tetap **REQUIRED_UNMEASURED**.

## Revision dan fingerprint

Rangkaian implementasi dimulai pada `5d516ba` dan hasil audit final berada pada
`cae48cb32eba551a4399385ab191896ad3246447`. Commit dipisah berdasarkan kemampuan: parser struktur,
validator chunk, parent index, builder, test tiap boundary, dokumentasi status, bugfix correctness,
dan optimasi lookup/resource limit. Commit hardening terakhir adalah `0f060ce`, `c9ce384`, `39ab6d8`,
`e935f4c`, `cd5549e`, `a7e2dd5`, `1150e8a`, dan `cae48cb`.

Fingerprint SHA-256 source yang diaudit:

| File | SHA-256 |
| --- | --- |
| `document/chunking/structural.rs` | `0a12655935dee56697ec2d9858e4b3bbf1e70c9d50344ef196b58b957deb2608` |
| `document/chunking/builder.rs` | `7af6f86fe414e2cacaeb4b6f9ddd9052d19ef212afd8e98be966594be649c643` |
| `document/chunking/parents.rs` | `502a46451d6d8d5c9735fc47d4b2f6f276d86544cf0f8b9530f38e94a49c4471` |
| `domain/chunks.rs` | `03b95550363e1cdca6a67239550649d421b77c994f7b081f66c28c9e37c0e5a4` |

## Kontrak yang aktif

Parser struktur mengenali heading berjangkar pada baris untuk dokumen, lampiran, penjelasan, bab,
bagian, paragraf, pasal, ayat, dan butir. Referensi `Pasal` di tengah kalimat tidak dipromosikan menjadi
node. Form-feed keluaran parser PDF diperlakukan sebagai page boundary; body ayat boleh panjang tanpa
terpotong oleh batas panjang heading. Lampiran/penjelasan menjadi ancestor bagi bab dan pasal di
dalamnya. Setiap node membawa ID stabil, ordered children, parent ID, serta rentang byte normalized dan
raw yang diverifikasi.

Builder mempertahankan bagian teks milik node, termasuk preamble, dan membagi unit panjang pada batas
UTF-8 dengan preferensi page, kalimat, lalu whitespace. Ukuran, minimum split, overlap, jumlah chunk,
dan kedalaman parent merupakan konfigurasi yang masuk fingerprint. Tokenizer diinjeksikan dan wajib
menghasilkan ID serta token count nonnol. Chunk mengikat source blob, text artifact, provision version,
structure refs, parent refs, text hash, config hash, dan kedua rentang sumber. Parent graph wajib memiliki
satu document root, tidak boleh siklik/hilang, dan seluruh edge traversal dibatasi.

Normalizer sekarang membawa raw byte length dan menyediakan pemeriksaan integritas mandiri untuk
schema/hash, coverage, urutan span, batas offset, serta bentuk lebar transform. `raw_cover` memakai
binary search dan menolak projection di luar source span. Parser tetap harus menerima output normalizer
yang sudah divalidasi terhadap raw text pada boundary producer; pemeriksaan mandiri tidak menggantikan
canonical replay dengan raw sumber.

## Hasil verifikasi

Suite final dijalankan dengan dependency terkunci/offline dan PDFium lokal:

```powershell
$env:REGULAGRAPH_PDFIUM_LIBRARY = "C:\Python312\Lib\site-packages\pypdfium2_raw\pdfium.dll"
$env:REGULAGRAPH_PDFIUM_CORE_VERSION = "126.0.6462.0"
cargo test --workspace --locked --offline
cargo clippy --workspace --all-targets --locked --offline -- -D warnings
```

Hasilnya **42 unit test dan satu native integration test PASS**; satu interop fixture C01 tetap ignored
secara eksplisit karena dijalankan melalui harness tersendiri. Clippy seluruh target PASS tanpa warning.

Agent `verify_c01` melakukan audit read-only. Audit pertama menemukan sepuluh counterexample pada panic
batas UTF-8, heading setelah form-feed, ayat panjang, ancestry lampiran, depth/root parent, sibling
overlap, token count kosong, mapping raw palsu, alokasi split sebelum cap, dan lookup kuadratik. Setelah
perbaikan, audit ulang memberi verdict **PASS scope library** dengan **11/11 pemeriksaan independen PASS**.
Tidak ada source produksi yang diubah oleh verifier.

Diagnostic non-acceptance untuk 1.000/2.000/4.000 pasal menghasilkan waktu parse 32/74/203 ms dan chunk
71/154/297 ms pada debug run verifier. Angka ini hanya memeriksa pola scaling setelah indeks child/node
dan binary-search mapping ditambahkan; ia bukan benchmark resmi dan tidak boleh dibandingkan dengan gate
required.

## Batas pembuktian dan pekerjaan berikutnya

Pola heading belum dinilai terhadap gold structure set 1.000 halaman, sehingga
`PARSING.STRUCTURE_F1` dan `PARSING.CRITICAL_TOKENS` belum terukur. Token counter test bukan tokenizer
produksi. Workload transform 100 MiB, `CHUNKING.THROUGHPUT`, peak RSS delapan worker, exception linking,
tabel/sel, OCR, konversi `regulagraph.v1.Chunk`, wire parity, dan integrasi publication juga belum
diukur. Unit test tidak membuktikan dampak Recall@20 atau context completeness.

Tahap berikutnya mengintegrasikan parser-normalizer-structure-chunker ke worker batch terisolasi,
memetakan output ke kontrak C01, memilih tokenizer produksi melalui M01, dan membangun gold G01 untuk
menguji false-positive/false-negative struktur, pengecualian, tabel, serta parameter ukuran/overlap.
