# src/ingestion/src/document/chunking

Pembuatan unit teks berdasarkan struktur regulasi dengan hubungan ke konteks induk. Folder ini mendukung granularitas pencarian dan konteks ekstraksi yang dapat berbeda. Implementasi transformasi berada di Rust. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menentukan relevansi terhadap pertanyaan atau menyusun jawaban. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mempertahankan parent ID, identitas versi pasal, batas sumber, dan token count. Potongan panjang boleh dipecah dengan hubungan yang utuh; ukuran dan overlap menjadi parameter evaluasi, bukan angka tetap tanpa pengukuran.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [builder.rs](builder.rs), [mod.rs](mod.rs), [parents.rs](parents.rs), [structural.rs](structural.rs).

## Benchmark dan perhatian performa

**CHUNKING.** Ukur cakupan pemetaan chunk ke sumber/induk, proporsi ketentuan-pengecualian yang terpisah, Recall@k bukti, jumlah token, serta waktu ingestion. Gate: tiap chunk terbit punya parent/source/version yang valid. Bandingkan struktur+konteks induk dengan baseline pada corpus yang sama; ambang kualitas/latency wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml).

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Parser struktur hukum, builder chunk source-mapped, validator record, parent index acyclic, dan proyeksi structure/chunk ke C01 aktif sebagai library Rust. Builder mempertahankan preamble, memecah unit panjang pada batas UTF-8/kata/kalimat, memakai tokenizer yang disuntikkan, dan menyimpan ancestry sebagai ID tanpa menduplikasi teks induk. Table reconstruction, exception linking lintas chunk/dokumen, tokenizer produksi, worker isolation, gold structure set, dan acceptance benchmark belum aktif. Unit test membuktikan invariant deterministik kecil, bukan target kualitas atau latency corpus.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [builder.rs](builder.rs) | Integrasikan tokenizer produksi dan konversi `ChunkView` ke batch wire; tambahkan linking exception yang dibuktikan gold. | Uji parity token, tabel, exception lintas chunk, 100 MiB transform, RSS, dan dampak retrieval. |
| [parents.rs](parents.rs) | Integrasikan batch hydration dengan context builder Go memakai snapshot yang dipin. | Uji retrieval hydration order, missing backend record, context duplication, serta `CONTEXT.BUILD_P95`. |
| [structural.rs](structural.rs) | Tambahkan struktur tabel/sel dan pola heading baru hanya berdasarkan gold corpus. | Ukur F1 batas pasal/ayat/huruf per strata dan simpan false-positive/false-negative. |
