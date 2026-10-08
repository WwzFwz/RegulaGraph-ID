# Durable initial INDEX inventory

Dokumen ini menjelaskan penyimpanan plan dan child job INDEX di PostgreSQL serta
batas integrasinya. Ini melanjutkan [persiapan plan](initial-index-writer.md),
bukan klaim bahwa daemon INDEX dan seluruh Hybrid GraphRAG sudah selesai.

`InitialIndexPlans.JobInventory` menghasilkan satu child job deterministik untuk
setiap plan yang sudah melalui admission sumber lengkap. `ScheduleIndexJobs`
mengunci reservation snapshot, memeriksa publisher fence, sumber CHUNK yang
belum dibatalkan, checksum request sumber dan metadata plan terdaftar. Inventory
dan semua child job dibuat dalam satu transaksi. Jika assignment terakhir gagal,
assignment serta job sebelumnya ikut rollback. Registrasi generation merupakan
prasyarat idempotent terpisah dan boleh tertinggal tanpa child job setelah gagal.

Migration `0015_index_job_inventory.up.sql` wajib diterapkan sebelum memakai
versi coordinator ini, termasuk generic claim yang kini menghindari child INDEX
berinventory. Migration forward-only tanpa backfill. Inventory/assignment tidak
boleh diubah atau dihapus; GC/retirement memerlukan desain tersendiri. Retry wajib
membawa binding, snapshot, auth scope, urutan job, sumber, dan plan identik.
Digest JSON adalah detail persistence internal Go; wire tetap C01 Protobuf.

`LoadIndexJobInventory` memeriksa checksum inventory dan mengembalikan urutan
assignment semula. `RestoreInitialIndexPlans` membaca ulang artefak dan authority
sumber/dictionary, menyusun ulang partisi, lalu membandingkan semua plan serta
child ID. Hilangnya satu plan, berubahnya sumber, atau tercabutnya authority
tidak boleh lolos hanya karena inventory database masih ada. Batas saat ini
256 child, 128 chunk per plan, dan 64 MiB total payload plan; overflow ditolak.

`ClaimIndexJob` hanya mengambil child terdaftar dengan publication aktif dan
sumber belum dibatalkan. Worker attempt dan fence meningkat saat reclaim;
terminal checkpoint dapat dipulihkan tanpa menghabiskan budget stage attempt.
Tanpa checkpoint terminal, attempt yang habis menjadi FAILED. Generic claim
tidak mengambil child ini, termasuk setelah STAGED. Job INDEX lama yang belum
memiliki inventory tetap mengikuti primitive generic sebelumnya.

Claim bukan bukti authority yang berlaku selamanya. Executor wajib memeriksa
ulang byte plan/sumber, cancellation dan publisher fence sebelum dispatch dan
commit. `VerifiedIndexOutput.Commit` kini mengikat output terverifikasi ke
assignment lewat `SaveIndexCheckpoint`: checkpoint dan STAGED atomik, source
checkpoint/cancellation serta publisher fence diperiksa kembali, dan lease dicek
lagi setelah menunggu lock. STAGED melepaskan lease, tetapi belum search-ready.
Batch memakai batas satu juta wire items yang sama dengan admission INDEX;
checkpoint/reference tetap memakai batas metadata default. Caller harus
melakukan `ExecuteBatch` dan registrasi output/dependency sebelum commit.

`InitialIndexJobProcessor` kini melakukan preflight job/publication, restore
inventory, dispatch, validasi output, registrasi, dan commit. Cache menahan satu
inventory admitted; authority job diperiksa pada setiap pemanggilan dan byte
batch/sumber diperiksa lagi sebelum worker. Cache tidak menyimpan semua teks
corpus; ukur peak RSS dan biaya restore ketika publication berganti.

Workflow `IndexExecutor` mengklaim satu job, membatasi waktu ke minimum deadline
call/lease, memantau cancellation dan menyimpan retry dengan backoff terbatas.
Lease tidak diperpanjang tanpa batas: pekerjaan melewati deadline dibatalkan,
kemudian diulang sesuai budget durable. Jika balasan commit hilang, processor
memeriksa checkpoint byte-identik dan status STAGED selama maksimal dua detik;
hanya bukti durable tersebut mengubah hasil menjadi sukses. Jika konfirmasi juga
gagal, error tetap dilaporkan dan state tersimpan menentukan claim selanjutnya.

Daemon `ingestion-worker` mengaktifkan kelompok INDEX dengan
`REGULAGRAPH_INDEX_ENABLED=true` dan merotasinya bersama kelompok lain. Konfigurasi
native INDEX pada worker Rust, migration0015/0016, shared artefact store, serta scope
coordinator yang sama dengan inventory merupakan prasyarat. Opsi ini tidak
menemukan corpus atau menjadwalkan plan otomatis. Scheduling masih melalui
library setelah population/plan admission; CLI persiapan inventory masih perlu
dirangkai. Pengumpulan seluruh output dan CLI publication tersedia melalui
[kontrak sumber/publication](index-source-publication.md). Tidak ada aktivasi
snapshot otomatis dari scheduling, claim, atau STAGED.

Ukur queue time, claim/schedule p95/p99, pool wait, contention, RSS dan recovery
sesuai [target required](../configs/benchmark-targets.yaml). Status target tetap
REQUIRED_UNMEASURED. [Laporan verifikasi](verification-report-index-jobs.md)
membedakan fixture storage dari acceptance produksi.
