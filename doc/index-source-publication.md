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

Jika inventory INDEX gagal terminal sebelum publication, operator dapat menutup
publication tersebut tanpa menghapus riwayat job/artefak:

```powershell
go run ./src/server/cmd/cli abort-index -corpus corpus:example -publication publication:example
```

Command memerlukan inventory admitted dalam corpus yang disebutkan dan DSN.
Coordinator meneruskan abort ke transaksi PostgreSQL yang menolak state published
serta ledger backend planned/applied yang belum dikompensasi. Command tidak
mengompensasi backend, mereset retry, atau mengubah active snapshot. Setelah sukses,
buat preparation dengan publication/generation baru dan bekukan ulang seluruh
dependency snapshot. Reusing konfigurasi tidak berarti statistics snapshot lama
dapat dipakai. Replay abort pada ABORTED mengembalikan error; jika stdout hilang,
periksa state database, jangan menyimpulkan commit gagal. Reservation tanpa
inventory belum ditangani perintah ini. Abort saat worker masih hidup dapat
membuat output kerja menjadi tidak dapat di-commit; hentikan coordinator milik
run terlebih dahulu agar tidak membuang compute.

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

`publish-index` mengonsumsi inventory yang sudah ada; tidak menciptakan corpus,
menjalankan anotasi gold, atau mengarang manifest statistik.

## Menyiapkan snapshot dari CHUNK nyata

`prepare-snapshot` menerima pilihan job dan artefak CHUNK yang sudah terdaftar.
Source job bukan child INDEX. Gunakan seluruh pilihan sumber untuk populasi,
bukan file PDF mentah atau direktori unduhan:

```powershell
go run ./src/server/cmd/cli prepare-snapshot -corpus corpus:example -publication publication:example -generation generation:example -auth-scope operator:corpus -source "job:document=artifact:document-batch:HASH" -out artifacts/preparation-example
```

Ulangi `-source` untuk semua pasangan job/artifact, maksimum 256. DSN dan root
artefak sama seperti publication; scope harus sama dengan coordinator CHUNK.
Perintah memeriksa bytes terdaftar, checkpoint, completeness, closure dan ID
chunk sebelum reservasi. Chunk ganda ditolak; batas populasi 32.768 chunk dan
64 MiB bytes DocumentBatch, bukan ukuran PDF. Overflow ditolak tanpa truncation.

Snapshot ID diturunkan dari daftar job/ref lengkap yang diurutkan, publication,
corpus, generation dan scope. Replay identik memakai reservation/binding sama;
input berbeda tidak dapat memakai reservation lama. Ini pilihan operator yang
eksplisit, bukan klaim bahwa seluruh dokumen portal/direktori sudah tercakup.

`corpus-facts.json` mengikuti format evaluator. `documents` menghitung distinct
raw source SHA-256 dari CHUNK terverifikasi, `chunks` menghitung ID chunk unik,
dan jumlah canonical entity/graph edge nol karena profil ini belum membentuk
graph. Counts bukan eligibility PASS. Hash byte JSON tepat menjadi manifest
hash snapshot; manifest terdaftar bersama dependency seluruh sumber asli.

Folder output baru berisi `snapshot.pb`, `source-001.pb` dan seterusnya,
`corpus-facts.json`, serta `sources.json` yang ditulis terakhir. File `.pb` adalah
C01 biner untuk [CLI population Rust](lexical-population.md). `sources.json`
merupakan peta operator job/artifact/nama ref, bukan wire schema alternatif.
Existing directory/file tidak ditimpa. Kegagalan dapat meninggalkan sebagian
data durable: ulangi input sama dengan folder output baru, jangan menganggap
keberadaan folder sebagai bukti sukses. Snapshot published tidak dipersiapkan
ulang sebagai publication baru.

Vocabulary/freeze Rust dan allocator dictionary Go dilanjutkan dengan
`prepare-index` di bawah. Persiapan snapshot sendiri tidak menjalankan inference
atau activation otomatis.

## Menjadwalkan embedding dan BM25

Sesudah vocabulary, `prepare-dictionary`, dan freeze Rust selesai mengikuti
[panduan population](lexical-population.md), jalankan:

```powershell
go run ./src/server/cmd/cli prepare-index -snapshot-directory artifacts/preparation-example -publication publication:example -collection regulagraph_example -auth-scope operator:corpus -ontology-version ontology:v1 -dictionary-ref dictionary-ref.pb -statistics-ref statistics-ref.pb -model-manifest artifacts/models/bge-m3-fp16-ort1220/model.pbjson -model-sha256 <sha256-byte-model.pbjson>
```

Gunakan DSN, artifact root dan Qdrant URL yang sama. Collection adalah nama
binding baru yang immutable. Nilai scope/ontology/model harus berasal dari
konfigurasi run, bukan menyalin contoh tanpa pemeriksaan. Snapshot, seluruh
sumber, statistik dan vocabulary harus berasal dari pilihan CHUNK yang sama.
Bootstrap operator ini menerima root dictionary keluaran `prepare-dictionary`;
planner library tetap mendukung rantai dictionary untuk integrasi berikutnya.

Go memeriksa registry dictionary, hash/schema statistik, population snapshot,
input policy, model generation dan seluruh source authority sebelum menyimpan
inventory. Statistik Rust diregistrasikan bersama dependency dictionary/sumber.
`EnsureArtifactDependencyManifest` hanya menerima replay set identik melalui
metode tersebut; ini bukan immutability global database, karena API replacement
lama masih tersedia. DF tidak dihitung ulang oleh Go: pemeriksaan consistency
tidak menggantikan pembuktian producer population maupun evaluasi kualitas.

Semua chunk dibagi menjadi child job stabil; `-chunks-per-batch` default 64,
rentang 1..128. Scheduling seluruh inventory atomik. Retry memakai input dan
publication sama; drift model atau dependency ditolak. Kegagalan persiapan bisa
meninggalkan artefak immutable, tetapi tidak inventory separuh terjadwal.

Perintah menulis `embedding-model.pb` ke direktori snapshot sebelum scheduling.
Byte C01 biner ini berisi manifest model yang sama dengan JSON native; SHA-256
biner berbeda dari pin JSON. Output sukses mengembalikan `worker_model_manifest`
dan `worker_model_manifest_sha256`, untuk `REGULAGRAPH_WORKER_EMBED_MANIFEST`
dan `REGULAGRAPH_WORKER_EMBED_MANIFEST_SHA256`. Ekspor identik dapat direplay;
file berbeda/parsial ditolak tanpa overwrite. Kegagalan sesudah ekspor belum
berarti job terjadwal; gunakan exit code dan output `status: scheduled`.

Jalankan native inference sesuai [panduan native](native-inference.md), lalu
worker Rust dengan ketiga variabel native pada [kontrak worker](index-build.md).
Coordinator Go memerlukan `REGULAGRAPH_INDEX_ENABLED=true`; sesudah seluruh
child STAGED, jalankan `publish-index`, kemudian `query-evidence`. Alur ini
menyediakan indeks dense/BM25 dengan bukti terpin; graph dan generation jawaban
penuh tetap pekerjaan terpisah. Status scheduled tidak berarti search-ready.

## Pengukuran dan bukti

Ukur binding I/O/RSS, queue time, admission/restore, recovery, waktu menunggu lock,
write/readback dan commit p95/p99 secara terpisah. Target tetap
[benchmark-targets.yaml](../configs/benchmark-targets.yaml), status
REQUIRED_UNMEASURED. [Laporan verifikasi](verification-report-source-publication.md)
memisahkan bukti fixture/integritas dari kualitas model dan acceptance produksi.

[Run native lintas runtime](verification-report-native-index.md) telah memeriksa
population/INDEX/publication/query dengan BGE-M3 lokal dan source fixture Rust.
Reader Go/Rust menerima vendor DocumentBatch dan alias typed exact yang sama;
source envelope mempertahankan label sumber serta seluruh checks provenance.
Run seluruh corpus PDF, graph dan jawaban penuh belum dibuktikan oleh tes tersebut.
