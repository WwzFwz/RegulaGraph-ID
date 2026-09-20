# Keputusan 0006: PDFium sebagai kandidat parser PDF teks I01

Dokumen ini mencatat pemilihan engine native untuk prototype parser dokumen. Perannya membatasi implementasi
I01 pada kandidat yang sudah diuji tanpa menyatakan parser produksi selesai. Status: **diterima untuk
prototype I01 pada 2026-09-20; penerimaan produksi menunggu gold quality, mapping, memory, dan benchmark
pipeline lengkap**.

## Konteks

Corpus D01 berisi PDF teks, scan, dan campuran dengan ukuran serta jumlah halaman yang sangat bervariasi.
Parser harus cepat, tidak menggantung pada file besar, menyediakan text/page/object/coordinate API, dapat
diikat ke Rust, dan dapat didistribusikan dengan kewajiban lisensi yang jelas. Target required tetap berada
di [benchmark-targets.yaml](../../configs/benchmark-targets.yaml).

Profiler M01 membandingkan pypdf 5.6.0, PyMuPDF 1.26.4/MuPDF 1.26.7, dan pypdfium2 4.30.0/PDFium
126.0.6462.0 pada sampling inventory yang sama. Bukti lengkap berada di
[laporan M01](../verification-report-m01-pdf-profile.md).

## Keputusan

I01 memakai PDFium C API sebagai kandidat utama parser PDF teks. Rust memiliki wrapper aman untuk ownership
document/page/text-page, timeout/cancellation di boundary worker, dan konversi hasil ke domain document.
Binding Python pypdfium2 tetap tooling eksperimen; ia bukan dependency runtime Rust.

MuPDF dipertahankan sebagai pembanding diagnosis pada gold set. pypdf dihentikan sebagai kandidat produksi
karena satu dokumen nyata tetap timeout pada 180 detik dan tail latency jauh lebih buruk. Tidak ada engine yang
boleh menerbitkan halaman scan sebagai teks kosong yang dianggap lengkap; halaman tersebut diarahkan ke jalur
OCR yang masih harus dipilih.

## Alasan

Pada run 120 dokumen, PDFium menyelesaikan 27.486 halaman tanpa error dalam 63,349 detik; MuPDF menyelesaikan
sampel sama dalam 70,467 detik. Page count dan klasifikasi heuristic keduanya identik pada seluruh dokumen;
109/120 dokumen memiliki jumlah karakter persis sama. PDFium mencatat page p95 17,341 ms dibanding MuPDF
33,845 ms pada run tersebut.

Lisensi juga menjadi faktor distribusi. Dokumentasi pypdfium2 menyatakan binding Apache-2.0/BSD-3-Clause dan
PDFium berlisensi gaya BSD beserta notice dependency; PyMuPDF/MuPDF menawarkan GNU AGPL atau lisensi komersial.
Review license/SBOM final tetap menjadi gate packaging O01.

## Konsekuensi implementasi

- Versi source/binary PDFium, build flags, checksum, dependency notices, dan ABI harus dipin dalam artefak build.
- Wrapper Rust harus menutup handle pada seluruh jalur sukses/error dan tidak membagikan page lifetime melewati
  document owner.
- Output awal wajib menyimpan page number, block/character coordinate, raw text identity, dan status error per
  halaman; normalisasi membangun mapping UTF-8 terpisah.
- G01 memberi label reading order, pasal/ayat, tabel, nomor, negasi, multi-column, dan 11 dokumen dengan selisih
  karakter antar-engine.
- Required benchmark baru dapat dinilai setelah parsing, normalisasi, chunking, source mapping, peak RSS, serta
  workload dan hardware referensi tersedia. Hasil profiler M01 tidak mengubah target.

Jika PDFium gagal quality gate, tidak memenuhi memory/latency pipeline, atau binding distributable tidak dapat
direproduksi, keputusan ditinjau ulang dengan MuPDF dan kandidat lain pada corpus/gold/config yang sama.
