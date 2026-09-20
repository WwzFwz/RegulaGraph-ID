# src/server/internal/adapters/storage

Penyimpanan serta pembacaan dokumen asli dan artefak sumber melalui referensi lokasi yang stabil. Adapter Go memakai koneksi yang dipakai ulang. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menjalankan parsing atau menjadi tempat penyimpanan metadata aplikasi tanpa kontrak. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menjaga hash konten, atomic write, izin akses, dan pemisahan path pengguna dari path penyimpanan. Dokumen asli tetap tersedia bagi provenance dan pemrosesan ulang.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[files.go](files.go) menyediakan local FileStore dengan `os.Root` confinement, scoped relative key, streaming SHA-256, byte-count verification, cancellation, file fsync, atomic no-replace publish, immutable reuse, dan penolakan path traversal/symlink. Pembacaan batch in-memory memeriksa ukuran file sebelum hashing, membatasi stream terhadap ukuran referensi, lalu memverifikasi hash kembali setelah alokasi terbatas; mismatch deterministik ditandai sebagai pelanggaran integritas. Directory chain di-fsync pada platform yang mendukungnya. Windows tidak mengizinkan directory-handle sync melalui API ini, sehingga power-loss durability entry direktori adalah batas deployment/backup O01 dan tidak diklaim lulus oleh S01. [files_test.go](files_test.go) memeriksa jalur correctness utama tanpa mengklaim throughput produksi.

## Benchmark dan perhatian performa

**SOURCE.** Ukur keberhasilan akuisisi, bytes/detik, retry, dan waktu p50/p95 per sumber. Gate: konten memiliki hash dan asal yang terlacak; unduhan parsial tidak diterbitkan sebagai sumber lengkap.

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Local artifact adapter S01 sudah aktif untuk write/read immutable terverifikasi. Retention-aware cleanup, object-store production, process-crash injection, dan benchmark bytes/detik/peak RSS masih belum diukur atau diimplementasikan sesuai paket U01/O01. Status anak dijelaskan pada header masing-masing; unit test tidak membuktikan target performa.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [files.go](files.go) | Pertahankan write/read immutable; tambahkan retention-aware cleanup setelah read-lease policy O01 tersedia. | Perluas symlink/process-crash/short-write injection lintas platform; ukur bytes/s dan peak buffers. |
