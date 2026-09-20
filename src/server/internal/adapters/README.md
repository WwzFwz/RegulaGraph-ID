# src/server/internal/adapters

Adapter teknologi untuk database, penyimpanan berkas, dan inference model. Folder ini mewujudkan kebutuhan akses eksternal dari komponen aplikasi. Seluruh adapter di sini menggunakan Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak memutuskan makna entitas, relevansi bukti, atau kebijakan jawaban. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mengekspos kontrak sempit kepada pemakai, mengubah exception vendor menjadi kegagalan yang dapat ditangani, dan mengelola resource secara eksplisit. Tidak ada koneksi atau model loading saat inisialisasi paket.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Subfolder: [inference/](inference/README.md), [neo4j/](neo4j/README.md), [postgres/](postgres/README.md), [qdrant/](qdrant/README.md), [storage/](storage/README.md), [worker/](worker/README.md).

Folder ini baru mendefinisikan batas komponen; belum ada implementasi runtime.

## Benchmark dan perhatian performa

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.
