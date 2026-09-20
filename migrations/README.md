# migrations

Migrasi schema persisten yang dikelola aplikasi, dimulai dari metadata PostgreSQL sesuai rancangan arsitektur. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak menjalankan ingestion, memperbarui embedding, atau mengganti migrasi dengan perubahan database saat import. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Anak menyatakan urutan, kompatibilitas aplikasi, dampak data, dan strategi pemulihan. Migrasi lintas backend tidak diasumsikan atomik dan harus menyebut target backend dengan jelas.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

[0001_storage_foundation.up.sql](0001_storage_foundation.up.sql) membentuk schema S01 untuk corpus state, artefak immutable, job/lease/fence/checkpoint, canonical identity dan resolution decision, dependency/lookup revision, publication generation/receipt/operation ledger, snapshot pointer, outbox, serta read lease. [0002_job_retry_schedule.up.sql](0002_job_retry_schedule.up.sql) menambah waktu eligibility retry, batas delapan attempt default, dan indeks claim stage/state. [0003_job_stage_attempts.up.sql](0003_job_stage_attempts.up.sql) memisahkan attempt global monotonik dari budget retry setiap stage. [0004_checkpoint_terminal_status.up.sql](0004_checkpoint_terminal_status.up.sql) menyimpan outcome terminal worker bersama checkpoint agar recovery tidak menebak hasil PARSE parsial sebagai sukses. [0005_registry_operation_idempotency.up.sql](0005_registry_operation_idempotency.up.sql) menambah operation ledger, exact identity scope/key, tipe entitas, serta status pembuatan untuk allocator canonical identity K01. Runner Go mencatat checksum setiap file dan menolak version drift.

Migration 0002 kompatibel dengan row lama melalui default `-infinity` dan delapan attempt. Migration 0003 mengisi `stage_attempt` dari attempt aktif agar upgrade tidak memberi retry tambahan secara diam-diam; handoff stage berikutnya mereset counter stage menjadi satu sambil menaikkan attempt global untuk registry worker. Migration 0004 mempertahankan checkpoint lama sebagai `NULL`; row lama tersebut tidak recovery-eligible pada attempt maksimum dan tidak pernah diartikan sebagai outcome sukses. Migration 0005 mengisi keputusan lama dari canonical row yang dirujuk; orphan menggagalkan migration agar provenance tidak direka. Natural key tekstual lama dipertahankan untuk kompatibilitas, sedangkan operation header sintetis memakai payload hash lama dan hanya menjadi jejak audit, bukan bukti replay claim asli. `ALTER TABLE`, backfill, dan pembuatan indeks berjalan transaksional dan dapat menahan write pada tabel besar; rollout produksi perlu strategi online sebelum traffic. Kegagalan statement membatalkan seluruh migration; recovery adalah memperbaiki penyebab lalu replay file ber-checksum sama, bukan mengedit revision yang sudah tercatat.

## Benchmark dan perhatian kualitas

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

Lihat [kebijakan benchmark](../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Migration fondasi S01 dan registry K01 sudah aktif. Schema kosong, replay checksum, upgrade valid dari revision 0004, kompatibilitas natural key tekstual lama, serta rollback pada orphan telah diuji terhadap PostgreSQL aktual. Migration ini hanya mencakup transaksi lokal PostgreSQL; mutation/rollback lintas Neo4j dan Qdrant memakai publication protocol. Upgrade dari release produksi tetap memerlukan rehearsal pada snapshot produksi; benchmark performa tetap REQUIRED_UNMEASURED.

## Pekerjaan berikutnya dan integrasi

Migration berikutnya harus memakai nomor baru dan menjelaskan kompatibilitas, backfill, lock impact, serta recovery. Uji upgrade dari revision sebelumnya ketika schema telah dirilis; jangan mengedit checksum migration yang sudah diterapkan atau menganggap transaksi ini mencakup Neo4j/Qdrant.
