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

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [html.rs](html.rs) | Parse regulatory HTML content into source-mapped blocks without treating navigation as law text. | Test lists/tables/encoded characters and malformed markup; preserve raw artifact and mapping provenance. |
| [ocr.rs](ocr.rs) | OCR selected pages with engine/model/config identity, coordinates, confidence and explicit unreadable-page status. | Measure CER/WER and accuracy of numbers/negation plus pages/s and RSS; retain original page images/locators. |
| [pdf.rs](pdf.rs) | Extract page text, reading order, blocks/tables and source locators; route only insufficient-text pages to OCR. | Evaluate digital/scanned/mixed strata, multi-column ordering and page failures; measure throughput and memory on large files. |
