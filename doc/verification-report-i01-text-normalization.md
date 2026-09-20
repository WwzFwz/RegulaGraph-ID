# Verifikasi normalisasi teks I01

Dokumen ini mencatat bukti implementasi normalizer teks Rust pada 2026-09-20. Perannya mengikat kontrak
mapping byte, kebijakan transformasi, test, dan review agent independen ke revision yang dapat ditinjau. PASS
di sini hanya berlaku untuk scope library normalizer; fidelity corpus terhadap gold, worker/wire, struktur,
chunking, throughput workload, serta peak RSS tetap **REQUIRED_UNMEASURED**.

## Revision dan kontrak

Implementasi berada pada commit `483ab61`; cleanup lint Rust terkait pada `9abbec8`; dokumentasi status
komponen pada `4773729`; dan kemajuan rencana pada `acdd2a1`. SHA-256 `text.rs` yang direview adalah
`eeeb9a711ed5a6270aee3e59d4d74ae8c816792ba815f57b918e3c0014bb4f62`.

Normalizer menerima UTF-8 dan konfigurasi versioned untuk batas input/mapping, collapse whitespace
horizontal, ekspansi ligature Unicode, dan penghapusan soft hyphen. Ia selalu menyatukan CRLF/CR menjadi LF.
Output membawa schema version, hash raw/normalized, fingerprint konfigurasi, teks normalized, serta mapping
monoton raw-ke-normalized yang menutup semua byte. Deletion memakai normalized span kosong; offset lain tetap
start-inclusive/end-exclusive pada batas karakter UTF-8.

Validator mengikat schema, hash, config, ukuran input, jumlah span, urutan/coverage, semantik tiap transform,
dan canonical replay output lengkap. Karena replay wajib identik, producer tidak dapat menghindari kebijakan
dengan memecah CRLF atau run whitespace menjadi span kecil yang masing-masing tampak valid. `raw_cover()`
memproyeksikan span normalized non-kosong ke raw cover minimal dan memakai arithmetic terperiksa.

Transformasi ini sengaja tidak melakukan dehyphenation lintas baris, Unicode compatibility normalization
umum, penghapusan header/footer, parafrasa, atau koreksi ejaan. Negasi, angka, tahun, unit, punctuation,
paragraph break, form-feed halaman, dan hyphen terlihat dipertahankan sampai gold mendukung aturan tambahan.

## Hasil verifikasi

Suite resmi dijalankan dengan dependency terkunci/offline serta binary PDFium lokal agar regression parser
native tetap tercakup:

```powershell
$env:REGULAGRAPH_PDFIUM_LIBRARY = "C:\Python312\Lib\site-packages\pypdfium2_raw\pdfium.dll"
$env:REGULAGRAPH_PDFIUM_CORE_VERSION = "126.0.6462.0"
cargo test --workspace --locked --offline -- --nocapture
cargo clippy --workspace --all-targets --locked --offline -- -D warnings
```

Hasil akhir adalah 17 unit test dan satu integration test native PASS; satu test interop C01 diabaikan secara
eksplisit karena memakai harness fixture tersendiri. Clippy seluruh target PASS tanpa warning.

Agent `verify_c01` bekerja read-only dan memberi verdict **PASS untuk scope library normalizer**. Sebanyak
14/14 counterexample independen lulus, termasuk 3.375 kombinasi UTF-8, tamper mapping/hash/config/schema,
minimal raw cover, overflow, transform yang dinonaktifkan, input limit, mapping-count cap, serta bypass split
CRLF/whitespace. Batas span diterapkan sebelum penambahan span baru dan masuk ke fingerprint konfigurasi.

## Batas pembuktian dan kelanjutan

Test belum mengukur target `RUST.TEXT_TRANSFORM` minimal 10 MiB/detik pada workload 100 MiB yang juga wajib
mencakup structural chunking dan source mapping. Biaya canonical replay untuk artefak yang dibaca ulang,
peak RSS, corpus PDF besar, dan kualitas legal critical token terhadap gold juga belum diukur. Hasil unit dan
counterexample tidak boleh dilaporkan sebagai PASS benchmark produksi.

Tahap berikutnya memetakan struktur/span hasil parser dan normalizer ke artefak C01, menjalankannya dalam
worker terisolasi, lalu membangun struktur hukum serta chunk dengan parent context. G01 harus menyediakan
label critical token, line-break hyphen, header/footer, tabel, multi-column, dan locator sebelum aturan
normalisasi baru diaktifkan.
