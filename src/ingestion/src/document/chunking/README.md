# src/ingestion/src/document/chunking

Pembuatan unit teks berdasarkan struktur regulasi dengan hubungan ke konteks induk. Folder ini mendukung granularitas pencarian dan konteks ekstraksi yang dapat berbeda. Implementasi transformasi berada di Rust. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menentukan relevansi terhadap pertanyaan atau menyusun jawaban. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mempertahankan parent ID, identitas versi pasal, batas sumber, dan token count. Potongan panjang boleh dipecah dengan hubungan yang utuh; ukuran dan overlap menjadi parameter evaluasi, bukan angka tetap tanpa pengukuran.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [mod.rs](mod.rs), [parents.rs](parents.rs), [structural.rs](structural.rs).

## Benchmark dan perhatian performa

**CHUNKING.** Ukur cakupan pemetaan chunk ke sumber/induk, proporsi ketentuan-pengecualian yang terpisah, Recall@k bukti, jumlah token, serta waktu ingestion. Gate: tiap chunk terbit punya parent/source/version yang valid. Bandingkan struktur+konteks induk dengan baseline pada corpus yang sama; ambang kualitas/latency wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml).

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Ini adalah scaffold struktur, dokumentasi, dan build lintas bahasa. Belum ada pipeline, database adapter, transport worker, atau model yang aktif. Go entry point hanya memberi status scaffold; Rust dan C++ menyediakan target library; protobuf belum memiliki message/service; tooling Python belum menjalankan model. Keberhasilan build tidak menyatakan target latency atau akurasi tercapai.
