# src/server/internal/adapters/neo4j

Adapter persistensi node, relasi, provenance, dan pembacaan graph melalui Neo4j. Adapter Go memakai koneksi yang dipakai ulang. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Keputusan canonicalization berada di resolution; strategi traversal berada di retrieval/graph. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memakai query berparameter, constraint identitas, batch transaksi, dan pemetaan hasil yang stabil. Hindari query tak berbatas dan penghapusan bukti bersama ketika satu sumber dicabut.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [store.go](store.go).

## Benchmark dan perhatian performa

**GRAPH.** Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [store.go](store.go) | Implement parameterized batched graph writes/readback with corpus, visibility and support identity constraints. | Test replay, shared-support retention and search-ready receipts on real Neo4j; profile query plans and fan-out. |
