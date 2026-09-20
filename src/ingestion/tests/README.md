# src/ingestion/tests

Integration test crate ingestion yang memerlukan boundary eksternal nyata. Folder ini memverifikasi interaksi
Rust dengan binary/native engine tanpa memindahkan implementasi produksi ke test.

## Batas tanggung jawab

Test di sini boleh membuat fixture lokal deterministik dan memuat library yang lokasinya diberikan oleh
environment. Ia tidak mengunduh binary saat test, tidak mengakses jaringan, dan tidak menganggap test yang
di-skip sebagai bukti integrasi. Unit logic tetap berada dekat modul sumber; acceptance lintas service berada
di folder tests root.

## Peran dan integrasi anak

[pdf_parser.rs](pdf_parser.rs) memuat PDFium melalui boundary produksi, memverifikasi binary/input hash,
menolak binding kedua dalam proses, mem-parsing PDF sintetis, lalu menjalankan processor PARSE sampai persistence dan verified reload `DocumentBatch`. Set `REGULAGRAPH_PDFIUM_LIBRARY` ke file library
native dan `REGULAGRAPH_PDFIUM_CORE_VERSION` ke versi build untuk menjalankan jalur native. Tanpa keduanya,
test melaporkan skip eksplisit agar build portability tidak memalsukan ketersediaan binary.

## Benchmark dan perhatian kualitas

Integration test ini menguji correctness kecil, bukan throughput, RSS, reading order regulasi, atau keamanan
file arbitrer. Target required tetap [benchmark-targets.yaml](../../../configs/benchmark-targets.yaml) dengan
status **REQUIRED_UNMEASURED** sampai workload I01 lengkap dijalankan lintas proses worker.

## Status

Native parser integration test aktif untuk PDF teks sintetis, malformed input, tamper hash, ownership binding global, serta parser-to-batch persistence. OCR, crash isolation lintas proses, corpus batch, source mapping gold, serta publication belum diuji di folder ini.
