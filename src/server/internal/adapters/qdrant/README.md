# src/server/internal/adapters/qdrant

Adapter akses indeks dense dan sparse pada Qdrant dengan pemetaan payload ke kontrak aplikasi. Adapter Go memakai koneksi yang dipakai ulang. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menentukan metode fusion bisnis atau menyamakan sparse BGE-M3 dengan BM25. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menjaga dimensi vector, nama representasi, filter payload, versi embedding, dan ID chunk. Penulisan batch dan penghapusan harus dapat diulang; pembacaan harus mematuhi snapshot corpus.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [client.go](client.go), [collections.go](collections.go),
[points.go](points.go), [search.go](search.go), [readback.go](readback.go),
[store_test.go](store_test.go), dan [readback_test.go](readback_test.go).

## Benchmark dan perhatian performa

**INDEX.** Ukur konsistensi ID/versi antarindeks, freshness lag, throughput indexing, ukuran indeks, dan retrieval Recall@k. Gate: penghapusan/upsert idempotent dan model/dimensi representasi cocok. Target kapasitas wajib mengikuti profil corpus/hardware dalam [target numerik wajib](../../../../../configs/benchmark-targets.yaml).

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Transport HTTP Qdrant v1.18 untuk membuat/memeriksa collection khusus generation,
upsert bernama `dense` dan `bm25`, serta query dengan filter corpus, generation,
snapshot, dan versi pasal kini tersedia. Input upsert memerlukan ID point yang
dialokasikan katalog tepercaya, record yang sudah dibuktikan dari sumber, dan
fence operasi yang masih hidup. `EnsureCollection` memeriksa bentuk vector dan
metadata generation; ack `wait=true` belum merupakan bukti seluruh point terlihat
di semua route. Tes HTTP lokal memeriksa shape dan fail-closed response.

`VerifyPoints` kini membaca ID point dalam batch berbatas dan membandingkan
payload penuh, dense yang dinormalisasi untuk Cosine, serta sparse BM25.
Ia dapat menemukan point hilang atau berubah pada route ber-`consistency=all`,
tetapi pemanggil masih harus memaginasi semua expected ID dan membuktikan
route/replica yang boleh melayani query.

Payload index, closure/recovery, bukti readback seluruh point dan replica,
PostgreSQL binding, writer coordinator, query hydration, Qdrant nyata, gold,
serta acceptance performa tetap belum tersedia. Tanggal berlaku dan kecukupan
multi-versi diperiksa setelah hydration oleh retrieval owner, bukan diasumsikan
dari filter kandidat backend.

Collection metadata memerlukan Qdrant minimal 1.16; bentuk API yang dipakai
ditargetkan pada [referensi resmi v1.18](https://api.qdrant.tech/v-1-18-x/api-reference/collections/create-collection).
Visibilitas snapshot dibatasi sampai `2^53-1` agar operator range numerik tidak
kehilangan presisi; nilai lebih besar gagal sebelum I/O dan memerlukan encoding
baru sebelum dapat dilayani. Filter pasangan menggunakan
[nested object](https://qdrant.tech/documentation/search/filtering/) agar satu
versi tidak tercampur dengan status/interval versi lain.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [client.go](client.go), [collections.go](collections.go) | Ikat konfigurasi fisik ke katalog generation dan periksa capability/read route sesudah perubahan koleksi. | Uji server Qdrant terpin, salah dimensi/modifier/metadata, restart dan route berubah. |
| [points.go](points.go), [readback.go](readback.go) | Sambungkan allocator ID, ledger operasi, closure dan paginasi pemeriksaan readback penuh sebelum publication. | Uji replay, partial write, stale fence, point hilang, dan replica tertinggal pada Qdrant nyata. |
| [search.go](search.go) | Hidrasi kandidat dan terapkan kebijakan temporal/versi final dengan budget overfetch eksplisit. | Ukur recall/latency per cabang dan kasus tanggal unknown/konflik. |
