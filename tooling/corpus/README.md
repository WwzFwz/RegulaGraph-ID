# tooling/corpus

Tooling offline untuk memilih sampel corpus yang reproducible, memeriksa karakteristik PDF nyata, dan
menyiapkan workload pembanding parser/OCR M01. Folder ini tidak menjadi parser produksi dan tidak diimpor
oleh runtime Go, Rust, atau C++.

## Batas tanggung jawab

Anak membaca inventory D01 yang sudah lolos integrity dan menulis hasil eksperimen ke artifacts. Ia boleh
mengukur text layer, indikasi image-only, error, halaman, waktu, serta distribusi sampel. Ia tidak menetapkan
kebenaran hukum, canonical identity, kualitas OCR, struktur pasal, atau hasil benchmark required tanpa gold
dan workload lengkap. Parser produksi tetap berada di src/ingestion.

## Peran dan integrasi anak

Setiap run mengikat inventory ID, record/blob hash, versi engine, konfigurasi, seed, timeout, dan hasil mentah.
Sampling harus deterministik serta mempertahankan portal dan ukuran file; kasus gagal tetap menjadi denominator.
Output dipakai untuk memilih kandidat M01 dan membentuk strata anotasi G01, bukan sebagai DocumentBatch produksi.
Direktori output bersifat immutable: profiler menolak menimpa manifest atau JSONL run yang sudah ada agar
consumer tidak pernah mencampur hasil baru dengan manifest lama. Atomic claim file mencegah dua proses
menerbitkan run secara bersamaan; lock yang tersisa setelah proses mati menandakan run tidak selesai.

## Isi saat ini

[profile_pdfs.py](profile_pdfs.py) menyediakan baseline pypdf terisolasi per dokumen dengan timeout keras,
bounded concurrency, klasifikasi heuristic text/scan-candidate/mixed/sparse, serta JSONL/result manifest.
[__init__.py](__init__.py) menandai package offline tanpa side effect.

## Benchmark dan perhatian kualitas

Ukur elapsed per dokumen/halaman, throughput, timeout/error, page count, dan text-character coverage. Jalankan
cold/warm terpisah dan jangan memakai heuristic strata sebagai label gold. Target required tetap berada di
[benchmark-targets.yaml](../../configs/benchmark-targets.yaml); baseline pypdf ini berstatus
**REQUIRED_UNMEASURED** sampai dibandingkan dengan engine native dan gold parsing.

## Status

Profiler baseline M01 aktif. Perbandingan MuPDF/PDFium, OCR, peak RSS, layout/source mapping, CER/WER,
table/column gold, serta pemilihan engine produksi belum selesai.
