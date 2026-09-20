# src/ingestion/src/document/chunking

Pembuatan unit teks berdasarkan struktur regulasi dengan hubungan ke konteks induk. Folder ini mendukung granularitas pencarian dan konteks ekstraksi yang dapat berbeda. Implementasi transformasi berada di Rust. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menentukan relevansi terhadap pertanyaan atau menyusun jawaban. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mempertahankan parent ID, identitas versi pasal, batas sumber, dan token count. Potongan panjang boleh dipecah dengan hubungan yang utuh; ukuran dan overlap menjadi parameter evaluasi, bukan angka tetap tanpa pengukuran.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [builder.rs](builder.rs), [mod.rs](mod.rs), [parents.rs](parents.rs), [structural.rs](structural.rs), dan [tokenizer.rs](tokenizer.rs).

## Benchmark dan perhatian performa

**CHUNKING.** Ukur cakupan pemetaan chunk ke sumber/induk, proporsi ketentuan-pengecualian yang terpisah, Recall@k bukti, jumlah token, serta waktu ingestion. Gate: tiap chunk terbit punya parent/source/version yang valid. Bandingkan struktur+konteks induk dengan baseline pada corpus yang sama; ambang kualitas/latency wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml).

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Parser struktur hukum aktif pada worker STRUCTURE dan tidak menerima atau membuat `provision_version_id`. Stage CHUNK menerima hanya `DocumentBatch` BIND lengkap, merekonstruksi struktur, lalu mencocokkan setiap versi melalui text artifact, span eksak, structural path kanonis, hierarchy provision, dan regulation per dokumen sebelum tokenisasi. Indeks binding gabungan mencegah pencarian kandidat kuadratik. Builder mempertahankan preamble, ancestry, mapping raw-normalized, serta batas UTF-8/kata/kalimat; execution budget global tidak mengubah fingerprint atau ID chunk. Batas 512 token ditegakkan memakai tokenizer Hugging Face yang dimuat sekali, dibatasi 128 MiB, dicocokkan dengan pin SHA-256 deployment, dan dijalankan tanpa truncation/padding. Table reconstruction, exception linking lintas dokumen, gold structure set, parity tokenizer dengan model terpilih, dan acceptance benchmark belum selesai. Unit test correctness tidak membuktikan target kualitas atau latency corpus.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [builder.rs](builder.rs), [tokenizer.rs](tokenizer.rs) | Pertahankan token cap/fingerprint dan tambahkan linking exception yang dibuktikan gold. | Uji parity BGE-M3, missing/foreign binding sebelum tokenisasi, tabel, exception lintas chunk, 100 MiB transform, RSS, dan dampak retrieval. |
| [parents.rs](parents.rs) | Integrasikan batch hydration dengan context builder Go memakai snapshot yang dipin. | Uji retrieval hydration order, missing backend record, context duplication, serta `CONTEXT.BUILD_P95`. |
| [structural.rs](structural.rs) | Tambahkan struktur tabel/sel dan pola heading baru hanya berdasarkan gold corpus. | Ukur F1 batas pasal/ayat/huruf per strata dan simpan false-positive/false-negative. |
