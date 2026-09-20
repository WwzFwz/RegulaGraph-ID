# Verifikasi boundary parser PDFium I01

Dokumen ini mencatat bukti implementasi boundary parser PDF Rust pada 2026-09-20. Perannya mengikat
dependency native, kontrak input/output, test, dan review agent independen ke revision yang dapat ditinjau.
PASS di sini hanya berlaku untuk boundary library yang diuji; worker isolation, OCR, struktur hukum,
normalisasi, chunking, kualitas terhadap gold, serta benchmark produksi tetap **REQUIRED_UNMEASURED**.

## Revision dan identitas runtime

Dependency PDFium dipin pada commit `f3a43a3`, implementasi parser pada `195a1d7`, integration test pada
`ba5feec`, dan sinkronisasi dokumentasi komponen pada `f3472e8`. Review memakai Rust/Cargo 1.87.0,
`pdfium-render` 0.9.4 dengan API feature `pdfium_6406`, `libloading` 0.8.9, dan PDFium core
126.0.6462.0 dari binary lokal pypdfium2. SHA-256 source `pdf.rs` yang direview adalah
`c0374b0ec61958d509ee4e7c10b808a1726b9c002ebd3bd1ae1143216a42c3ff`.

Target dan workload pada `configs/benchmark-targets.yaml` tidak diubah. Tidak ada angka target yang
dinyatakan tercapai oleh test correctness ini.

## Kontrak yang tersedia

`PdfParser::bind()` memvalidasi konfigurasi, format identitas versi, serta hash binary sebelum dan sesudah
dynamic loading. Karena `pdfium-render` memakai binding global, parser menserialisasi bind dan menolak
binding kedua dalam proses. Manifest hasil membawa versi schema, engine/binding/API feature, versi core yang
dideklarasikan, hash library, fingerprint konfigurasi, dan hash input.

`PdfParser::parse_file()` memvalidasi source/blob ID serta hash input sebelum membuka dokumen dan mengulang
hash setelah ekstraksi. Output memuat teks UTF-8 dokumen, span byte start-inclusive/end-exclusive, locator
blok top-left 0..1 yang memakai intersection CropBox/MediaBox dan rotasi intrinsik, hasil/status per halaman,
indikator image, latency halaman, status dokumen, serta manifest. Antarhalaman dipisahkan form-feed. Halaman
teks minim dibedakan menjadi `NeedsOcr` atau `Sparse`; kegagalan halaman dan limit tidak disamarkan sebagai
hasil lengkap.

Batas page count, byte teks halaman/dokumen, serta kedalaman XObject berada di konfigurasi dan masuk ke
fingerprint. Traversal XObject yang terpotong, ukuran halaman invalid, bounds invalid, halaman gagal dibuka,
dan pelanggaran limit menghasilkan status/error eksplisit.

## Hasil verifikasi

Perintah resmi berikut dijalankan dengan binary PDFium lokal dan dependency offline terkunci:

```powershell
$env:REGULAGRAPH_PDFIUM_LIBRARY = "C:\Python312\Lib\site-packages\pypdfium2_raw\pdfium.dll"
$env:REGULAGRAPH_PDFIUM_CORE_VERSION = "126.0.6462.0"
cargo test --workspace --locked --offline -- --nocapture
```

Hasilnya tujuh unit test dan satu integration test native PASS. Satu test interop C01 tetap ignored dengan
alasan eksplisit karena dijalankan oleh harness contract tersendiri. Integration test membentuk PDF teks
deterministik, memeriksa extraction/span/bbox/manifest, menolak hash salah dan malformed PDF, serta memastikan
binding kedua dalam proses ditolak.

Agent `verify_c01` bekerja read-only dan memberi verdict **PASS** setelah temuan locator diperbaiki. Overlay
independen menjalankan 14/14 counterexample dengan hasil PASS: binding global concurrent, hash/ID, input
UTF-8 dan separator antarhalaman, malformed/encrypted/zero-page, batas page/text/document, nested XObject,
MediaBox dengan origin bergeser, CropBox internal maupun lebih besar dari MediaBox, serta rotasi halaman.
Dua PDF corpus nyata berjumlah 17 dan 107 halaman juga lolos invariant struktur hasil dan span. Pemeriksaan
tambahan setelah hardening hash library meluluskan tiga regresi identity/hash/concurrent binding.

## Batas pembuktian

Mutasi DLL tepat di interval dynamic loading diperiksa secara statis, belum diinjeksi. Dua file corpus nyata
tidak memiliki gold untuk reading order, isi pasal/ayat, tabel, angka, negasi, atau ketepatan bbox visual.
Fixture dan counterexample berjalan di proses test, sehingga belum membuktikan timeout/crash containment pada
file arbitrer. Peak RSS, throughput pipeline Rust lengkap, latency p50/p95/p99, antrean worker, dan recovery
batch juga belum diukur.

## Pekerjaan berikutnya

I01 berikutnya mengintegrasikan parser ke worker process terisolasi dan artefak batch C01, lalu membangun
mapping raw-normalized, deteksi struktur hukum, routing OCR per halaman, dan chunk struktural dengan konteks
induk. G01 harus memberi label reading order, tabel, pasal/ayat, critical token, scan/mixed page, dan locator
untuk mengubah correctness boundary ini menjadi bukti kualitas. M01 tetap harus memilih engine OCR dan model
lain melalui quality, latency, throughput, memory, serta parity pada workload frozen.
