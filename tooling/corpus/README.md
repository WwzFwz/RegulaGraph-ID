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

[profile_pdfs.py](profile_pdfs.py) membandingkan pypdf, binding MuPDF, dan binding PDFium melalui proses
terisolasi per dokumen dengan timeout keras, bounded concurrency, klasifikasi heuristic
text/scan-candidate/mixed/sparse, serta JSONL/result manifest yang memakai sampel identik.
[prepare_gold_queue.py](prepare_gold_queue.py) menerbitkan antrean anotasi G01 yang deterministik dari PDF
primary pada inventory D01 yang diaudit. Manifest mengikat inventory ID, hash records, seed, jumlah per
portal/ukuran/tahun metadata, dan hash queue; setiap baris menyimpan sumber, record, path blob, dan status
UNREVIEWED. Tool menolak overwrite dan perubahan records, tetapi tidak membaca ulang byte PDF. Output ini
belum memiliki label hukum, split, atau corpus snapshot beku, sehingga bukan gold dataset.
[__init__.py](__init__.py) menandai package offline tanpa side effect.

## Benchmark dan perhatian kualitas

Ukur elapsed per dokumen/halaman, throughput, timeout/error, page count, dan text-character coverage. Jalankan
cold/warm terpisah dan jangan memakai heuristic strata sebagai label gold. Target required tetap berada di
[benchmark-targets.yaml](../../configs/benchmark-targets.yaml); baseline pypdf ini berstatus
**REQUIRED_UNMEASURED** sampai engine terpilih diintegrasikan ke Rust dan dinilai dengan gold parsing.

## Status

Pembanding pypdf/MuPDF/PDFium M01 aktif. Perbandingan OCR, peak RSS, layout/source mapping, CER/WER,
table/column gold, binding Rust, serta pemilihan engine produksi belum selesai.
Antrean kandidat G01 aktif; anotasi dan adjudikasi manusia belum selesai.
