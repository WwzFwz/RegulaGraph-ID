# src/ingestion/src/knowledge_graph/assembly

Penggabungan entitas yang telah diselesaikan identitasnya dan relasi menjadi perubahan graph yang konsisten. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengekstrak ulang makna atau menjalankan strategi pencarian pertanyaan. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memetakan endpoint ke canonical ID, mempertahankan banyak bukti untuk satu relasi, dan menghasilkan perubahan idempotent. Penghapusan satu sumber tidak boleh menghapus relasi yang masih didukung sumber lain.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [builder.rs](builder.rs), [mod.rs](mod.rs).

`builder.rs` kini menyediakan fungsi prapublikasi yang mengganti endpoint mention
berdasarkan keputusan LINK/CREATE RESOLVE terikat EXTRACT, memeriksa canonical yang
disediakan pembaca registry terverifikasi, dan mempertahankan seluruh support asli.
Keluaran masih membawa ID assertion/support dari ekstraksi dan belum boleh menjadi
`GraphDelta` siap publikasi: deduplikasi assertion canonical, remap support, closure,
dependency manifest, validasi keseluruhan delta/span terhadap byte teks sumber, dan
writer Neo4j belum aktif. Validasi ontology serta bentuk span EXTRACT/RESOLVE sudah aktif.

## Benchmark dan perhatian performa

**GRAPH.** Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status subkomponen ini hanya prapublikasi endpoint graph dan tes deterministik kecil;
ASSEMBLE worker, GraphDelta, mutasi backend, retrieval graph, gold dataset, dan
acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing;
tes library tidak membuktikan kebenaran semantik atau target latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [builder.rs](builder.rs) | Lanjutkan endpoint prapublikasi menjadi GraphDelta idempotent dengan canonical assertion key, remap support, closure, dan dependency manifest. | Uji duplikasi extraction, dua support satu assertion, penarikan satu support, dangling edge, dan full-rebuild equivalence. |
