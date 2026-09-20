# src/ingestion/src/document/normalization

Pembersihan artefak mekanis hasil parsing agar teks konsisten tanpa mengubah makna ketentuan. Implementasi transformasi berada di Rust. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Normalisasi pertanyaan ada di retrieval/query; penyamaan entitas ada di knowledge_graph/resolution. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menyimpan pemetaan teks bersih ke sumber. Negasi, nomor, tahun, satuan, struktur daftar, dan penanda pengecualian harus dipertahankan; perubahan dapat diaudit.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [mod.rs](mod.rs), [text.rs](text.rs).

## Benchmark dan perhatian performa

**PARSING.** Ukur ketepatan batas pasal/ayat, ketepatan urutan baca, CER/WER OCR pada sampel berlabel, dan detik/halaman per format. Gate: sumber dan lokasi teks tetap terlacak; nomor, negasi, dan pengecualian pada fixtures dipertahankan. Target statistik wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml); ukur dengan gold set yang memenuhi profil.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

[text.rs](text.rs) menyediakan normalisasi line ending, whitespace horizontal, ligature Unicode, dan soft hyphen dengan mapping byte raw-normalized yang lengkap, hash/fingerprint, limit input/mapping, canonical replay, serta proyeksi span normalized ke raw. Dehyphenation lintas baris dan penghapusan header/footer belum aktif karena memerlukan gold agar tidak mengubah istilah atau struktur hukum. Mapping artefak wire, integrasi worker, benchmark 100 MiB, dan acceptance produksi belum aktif.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [text.rs](text.rs) | Integrasikan mapping aktif ke artefak C01; evaluasi dehyphenation dan header/footer hanya dengan gold serta policy versioned. | Uji locator end-to-end dari chunk ke raw PDF, critical token, adversarial layout, throughput 100 MiB, dan peak RSS. |
