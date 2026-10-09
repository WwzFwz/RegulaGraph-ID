# src/server/internal/adapters/neo4j

Adapter persistensi node, relasi, provenance, dan pembacaan graph melalui Neo4j. Adapter Go memakai koneksi yang dipakai ulang. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Keputusan canonicalization berada di resolution; strategi traversal berada di retrieval/graph. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memakai query berparameter, constraint identitas, batch transaksi, dan pemetaan hasil yang stabil. Hindari query tak berbatas dan penghapusan bukti bersama ketika satu sumber dicabut.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[store.go](store.go) memiliki konfigurasi, binding generation dan transaksi Bolt
eksplisit; [schema.go](schema.go) memasang serta memeriksa uniqueness constraints.
[records.go](records.go) memproyeksikan GraphDelta C01 ke payload protobuf exact dan
adjacency bertipe. [write.go](write.go) menulis batch atomik dengan replay operation
ID/hash, sedangkan [readiness.go](readiness.go) memeriksa seluruh inventory dan
menutup generation terhadap penulisan baru. [store_test.go](store_test.go) menguji
boundary projection serta backend Neo4j nyata secara opt-in.

`Describe` memakai projection inventory yang sama dengan seal tanpa koneksi backend,
sehingga konflik lintas delta ditolak sebelum write. `description_test.go` menguji
union, konflik, replay ordering dan clone binding; domain memiliki binding hash v1.

Caller harus melakukan admission source/registry sebelum menyerahkan delta. Binding
mengikat corpus, generation, publication/fence, base snapshot, target sequence dan
registry revision. `New` tidak membuka koneksi; bootstrap memanggil `EnsureSchema`
secara eksplisit, lalu `ApplyGraphDelta` dan `VerifyAndSeal`. Proof backend bukan
receipt publication PostgreSQL atau izin melayani query. Kontrak lengkap, batas
resource dan pekerjaan integrasi ada di [graph store](../../../../../doc/neo4j-graph-store.md).

## Benchmark dan perhatian performa

**GRAPH.** Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Penulisan additive ke generation awal terisolasi, replay, shared support dan exact
verification/seal tersedia. Output Rust aktual telah diuji hingga PostgreSQL STAGED
dan Neo4j; lihat [bukti](../../../../../doc/verification-report-neo4j.md). Integrasi
catalog/write-intent PostgreSQL kini tersedia melalui [writer](../../indexing/graph_writer.go).
Receipt publication PostgreSQL, traversal query, closure incremental dan
readiness cluster belum tersedia. Tes fixture tidak membuktikan kualitas model,
coverage corpus atau target latency/throughput.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [store.go](store.go), [readiness.go](readiness.go) | Sambungkan proof generation ke receipt publication authoritative; tambah pembacaan untuk retrieval setelah admission snapshot. | Crash/retry antar-backend, stale fence, publication atomik pada pointer PostgreSQL dan isolasi snapshot. |
| [write.go](write.go), [records.go](records.go) | Tambah incremental closure/support changes dengan dependency manifest dan historical visibility yang sah. | Equivalence terhadap rebuild, retensi shared support dan versi lama; ukur fan-out, commit, peak RSS dan contention. |
