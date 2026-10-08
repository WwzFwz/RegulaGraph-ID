# Sumber snapshot dan publication indeks awal

Dokumen ini menjelaskan pengikatan CHUNK hasil daemon ke snapshot, pemulihan
output INDEX durable, dan publication profil dense/BM25. Kontrak ini melanjutkan
[inventory INDEX](index-job-inventory.md); tidak menyatakan graph, kualitas model,
atau seluruh milestone X01 sudah selesai.

## Input dan provenance

Daemon menghasilkan CHUNK tanpa snapshot publication. `BindInitialIndexSource`
membaca ulang bytes dan metadata terdaftar serta checkpoint CHUNK sukses.
`BindInitialSnapshotSource` membuat envelope DocumentBatch baru dengan snapshot,
request/config binder, dan dependency ke artefak asli. Semua record hukum, teks,
offset, ID chunk, serta dependency eksternal/lookup revision dipertahankan.
Artefak dan checkpoint asli tidak diubah. Binding hanya menerima sumber belum
bersnapshot dalam corpus/auth scope yang sama; bukan mekanisme reuse incremental.

Migration **0016 wajib diterapkan sebelum runtime baru**, termasuk reader
otoritas INDEX dan publication. Receipt immutable membekukan pasangan
original/bound per source job, serta satu snapshot/scope per publication.
Transaksi memeriksa source cancellation, publisher fence, metadata terdaftar dan
checkpoint asli. Snapshot harus identik baik binding dilakukan sebelum maupun
sesudah staging. Scheduling juga memeriksa snapshot yang sudah terikat.
Kegagalan setelah penulisan artefak boleh meninggalkan blob tak terpakai, tetapi
tanpa receipt commit blob tersebut belum memiliki otoritas sebagai sumber INDEX.

## Pemulihan dan hasil lengkap

Processor normal memakai checkpoint/STAGED atomik. Untuk checkpoint sukses lama
yang tersimpan sebelum transisi job selesai, recovery membaca ulang output,
plan, source authority, serta dictionary. Output dan konteks asli dipertahankan;
hanya checkpoint coordinator yang memperoleh ID/fence baru. Tidak ada inference
ulang. Checkpoint tidak didukung membutuhkan replan; artefak hilang/rusak yang
sudah dipastikan menjadi kegagalan integritas, bukan retry tanpa batas. Kegagalan
I/O sementara tetap dapat diulang. Reclaim tidak memakai ulang fence lama.

`LoadIndexJobOutputs` mengambil seluruh child dalam satu statement berbatas,
memeriksa ordinal/count, STAGED, cancellation, checkpoint/fence/producer/hash dan
referensi output. Child belum selesai tidak menghasilkan daftar sukses parsial.
`PrepareCompletedInitialIndex` kemudian memulihkan plan lengkap dan melakukan
admission bytes/source/vector kembali. Metadata STAGED sendiri tidak cukup.
Batas awal tetap 256 child, 128 chunk per batch, 16 MiB per artefak dan budget
admission agregat yang dijelaskan pada kontrak inventory; ini batas implementasi,
bukan perubahan workload benchmark atau janji seluruh corpus muat satu run.

## Operator publication

Setelah inventory dijadwalkan, semua child STAGED dan shared artifact store siap:

```powershell
go run ./src/server/cmd/cli publish-index -corpus corpus:example -publication publication:example -profile hybrid
```

Perintah memakai `REGULAGRAPH_POSTGRES_DSN`, `REGULAGRAPH_ARTIFACTS_DIR`,
`REGULAGRAPH_QDRANT_URL`, dan API key Qdrant bila dibutuhkan. Corpus dan endpoint
harus cocok dengan inventory. Deadline default 5 menit, maksimum 30 menit.
Exit 0 mengeluarkan JSON dengan status published dan SnapshotRef C01; exit 1
menandakan kegagalan operasional/integritas/output, exit 2 argumen tidak sah.
Jika output terminal gagal ditulis, publication mungkin sudah commit: ulangi
publication yang sama, jangan mengganti ID atau menghapus blob.

`PublishCompletedVectorIndex` menyusun manifest profil **dense + BM25** setelah
semua output terverifikasi, lalu memakai writer Qdrant/readback/receipt yang sama.
Profil ini belum Hybrid GraphRAG. Manifest yang sebelumnya mewajibkan Neo4j tidak
bisa diturunkan menjadi Qdrant saja pada retry. Published replay memeriksa
manifest yang sama dan tidak melakukan upsert ulang. Ini operasi data lokal,
bukan deployment layanan.

Tepat sebelum pointer aktif berubah, transaksi publication mengunci semua child
dan source job lalu memeriksa cancellation, jumlah, STAGED dan checkpoint fence
sukses. `NOWAIT` menghindari deadlock dengan writer yang sudah memegang job lock;
contention menghasilkan not-ready untuk retry. Cancellation setelah publication
commit tidak mengubah snapshot historis; update/retirement memerlukan protokol
tersendiri. Receipt backend tetap wajib dan intent gagal tetap tersedia untuk
pemulihan identik.

CLI persiapan snapshot/population/inventory dari pilihan dokumen resmi masih
perlu dirangkai agar operator tidak menyusun input library secara manual.
`publish-index` mengonsumsi inventory yang sudah ada; tidak menciptakan corpus,
menjalankan anotasi gold, atau mengarang manifest statistik.

## Pengukuran dan bukti

Ukur binding I/O/RSS, queue time, admission/restore, recovery, waktu menunggu lock,
write/readback dan commit p95/p99 secara terpisah. Target tetap
[benchmark-targets.yaml](../configs/benchmark-targets.yaml), status
REQUIRED_UNMEASURED. [Laporan verifikasi](verification-report-source-publication.md)
memisahkan bukti fixture/integritas dari kualitas model dan acceptance produksi.
