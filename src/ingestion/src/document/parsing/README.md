# src/ingestion/src/document/parsing

Konversi format sumber menjadi teks dan struktur dokumen, termasuk paragraf, pasal, ayat, halaman, dan tabel. OCR menjadi jalur untuk sumber yang tidak menyediakan teks memadai. Implementasi transformasi berada di Rust. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak melakukan ringkasan, ekstraksi relasi semantik, atau pemotongan untuk retrieval. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Parser anak harus mempertahankan urutan baca serta pemetaan teks ke halaman atau rentang sumber. Kemampuan dan ketidakpastian parser dilaporkan; fallback OCR tidak boleh menghapus bukti asli.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [html.rs](html.rs), [mod.rs](mod.rs), [ocr.rs](ocr.rs), [pdf.rs](pdf.rs).

## Benchmark dan perhatian performa

**PARSING.** Ukur ketepatan batas pasal/ayat, ketepatan urutan baca, CER/WER OCR pada sampel berlabel, dan detik/halaman per format. Gate: sumber dan lokasi teks tetap terlacak; nomor, negasi, dan pengecualian pada fixtures dipertahankan. Target statistik wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml); ukur dengan gold set yang memenuhi profil.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Ini adalah scaffold struktur, dokumentasi, dan build lintas bahasa. Belum ada pipeline, database adapter, transport worker, atau model yang aktif. Go entry point hanya memberi status scaffold; Rust dan C++ menyediakan target library; protobuf belum memiliki message/service; tooling Python belum menjalankan model. Keberhasilan build tidak menyatakan target latency atau akurasi tercapai.
