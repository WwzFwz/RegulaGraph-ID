# src/ingestion/src/document

Transformasi Rust atas sumber yang telah diambil coordinator Go: parsing native, normalisasi, chunk struktural dengan parent, deteksi perubahan, dan metadata versi. Komponen ini menyiapkan input knowledge graph serta indexing secara batch. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menentukan canonical entity lintas dokumen, strategi retrieval, atau jawaban pengguna. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak-anak mempertahankan source ID, content hash, lokasi teks, struktur induk, dan status parsing. Bedakan perubahan isi file dari perubahan keberlakuan ketentuan; hasil gagal tidak diterbitkan sebagai hasil lengkap.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [chunking/](chunking/README.md), [normalization/](normalization/README.md), [parsing/](parsing/README.md), [versioning/](versioning/README.md).

Berkas: [change_detection.rs](change_detection.rs), [mod.rs](mod.rs).

## Benchmark dan perhatian performa

**PARSING.** Ukur ketepatan batas pasal/ayat, ketepatan urutan baca, CER/WER OCR pada sampel berlabel, dan detik/halaman per format. Gate: sumber dan lokasi teks tetap terlacak; nomor, negasi, dan pengecualian pada fixtures dipertahankan. Target statistik wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml); ukur dengan gold set yang memenuhi profil.

**CHUNKING.** Ukur cakupan pemetaan chunk ke sumber/induk, proporsi ketentuan-pengecualian yang terpisah, Recall@k bukti, jumlah token, serta waktu ingestion. Gate: tiap chunk terbit punya parent/source/version yang valid. Bandingkan struktur+konteks induk dengan baseline pada corpus yang sama; ambang kualitas/latency wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml).

**VERSIONING.** Ukur ketepatan pemilihan versi pada pertanyaan bertanggal, kebocoran versi yang tidak berlaku, dan penanganan status tidak diketahui. Gate: tanggal observasi tidak menggantikan tanggal berlaku; fixtures perubahan/pencabutan/unknown menghasilkan status yang ditentukan label.

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Parser PDFium I01 aktif di [parsing/pdf.rs](parsing/pdf.rs) dan menghasilkan teks, locator ternormalisasi terhadap intersection CropBox/MediaBox serta rotasi halaman, status halaman, dan manifest yang terikat hash. Normalizer di [normalization/text.rs](normalization/text.rs) menghasilkan teks konservatif beserta mapping byte raw-normalized yang canonical. Struktur hukum, chunking, versioning, OCR, worker batch, gold dataset, dan acceptance produksi belum aktif. Fixture membuktikan boundary dasar, bukan kualitas corpus atau target latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [change_detection.rs](change_detection.rs) | Compare content and producer/dependency fingerprints, including negative lookup revisions, to emit incremental work plans. | Compare incremental vs clean rebuild on late references/model changes; measure reused work while preserving old/shared evidence. |
