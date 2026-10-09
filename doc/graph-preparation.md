# Persiapan graph melalui CLI

Dokumen ini menjelaskan `prepare-graph`: dari snapshot indeks terpublikasi dan
RESOLVE committed menjadi inventory ASSEMBLE yang dijadwalkan secara atomik.
Ia menyambungkan source preparation dengan [worker](graph-job-execution.md) dan
[publication](graph-publication.md), tanpa membuat keputusan canonical baru.

## Input dan pemakaian

Migration sampai 0023 harus tersedia. Snapshot aktif harus merupakan snapshot
indeks awal, bukan snapshot graph yang mewarisi indeks. Semua source job yang
tercantum dalam inventory indeks harus mempunyai checkpoint RESOLVE sukses,
artefak asli, intent/receipt keputusan bila nonempty, dan canonical assignment
yang dapat dieksekusi. Scope dan ontology harus sama dengan pipeline sumber.
RESOLVE boleh berasal dari revision berbeda yang tidak lebih baru daripada target.
[Reaffirmation](graph-reaffirmation.md) memeriksa ledger serta seluruh dependency
kandidat/dokumen sebelum membuat envelope turunan; keputusan asli tetap immutable.
Publication baru memilih current retained revision dan membekukannya melalui CAS.

```powershell
$env:REGULAGRAPH_POSTGRES_DSN = '<local DSN>'
$env:REGULAGRAPH_ARTIFACTS_DIR = '<artifact root shared with Rust worker>'
$env:REGULAGRAPH_ONTOLOGY_PATH = (Resolve-Path configs/ontology-v1.jsonc).Path
$env:REGULAGRAPH_ONTOLOGY_SHA256 = '<approved SHA256>'
go run ./src/server/cmd/cli prepare-graph `
  -corpus corpus:example -publication publication:graph-example `
  -snapshot snapshot:graph-example -base-snapshot snapshot:index-example `
  -auth-scope operator:corpus -timeout 5m
```

Ganti identitas contoh dengan identitas sumber aktual. `-base-snapshot` harus
sama dengan snapshot aktif; argumen ini mencegah perintah memilih parent berbeda
ketika operator mengulangnya. `-snapshot` menjadi target yang juga dipakai oleh
`publish-graph`. Environment tidak otomatis dibaca dari `.env`. Perintah tidak
membutuhkan API key model, endpoint Neo4j, ataupun proses embedding.

## Proses dan output

Go mem-pin parent, membaca inventory indeks dan memulihkan exact source receipts.
Receipt persiapan yang tidak menjadi anggota indeks diabaikan. Seluruh anggota
indeks wajib ditemukan; tidak ada pemilihan hanya dokumen yang kebetulan siap.
Preflight memverifikasi checkpoint, hash, historical resolution ledger, kandidat
registry dan assignment. DEFER, checkpoint gagal/hilang, cancellation, perubahan
konteks kandidat dan revision lebih baru dari target menghentikan persiapan.
Tahap ini belum mereservasi target.

Sesudah preflight, coordinator mereservasi target/fence, membekukan registry view,
mengikat envelope sumber ke parent, mengekspor canonical view, dan menulis plan
deterministik. Admission storage membaca ulang input dan authority. Seluruh child
job beserta inventory dibuat dalam satu transaksi. Perubahan registry/checkpoint,
lease kedaluwarsa atau publisher baru dapat membatalkan proses setelah preflight;
hasil preflight bukan izin permanen.

Exit 0 mengeluarkan JSON seperti:

```json
{"status":"scheduled","publication":"publication:graph-example","snapshot":"snapshot:graph-example","jobs":["job:assembly:<digest>"]}
```

`scheduled` berarti inventory durable, belum graph selesai atau search-ready.
Jalankan coordinator dengan `REGULAGRAPH_ASSEMBLE_ENABLED=true` dan konfigurasi
scope/ontology/artifact root yang sesuai. Sesudah semua child STAGED, jalankan
`publish-graph` dengan publication dan target yang sama, lalu gunakan
[query-evidence](query-evidence.md) atau [API](evidence-api.md).

Exit 2 berarti argumen/config tidak valid; exit 1 berarti preparation gagal.
Output error tidak mencetak DSN atau credential. Periksa status job sumber,
artefak, registry dan target sebelum retry. Lease operator dilepas dengan bounded
cleanup dan mempunyai expiry jika cleanup gagal.

## Retry, batas dan status

Ulangi argumen identik selama parent tetap aktif dan target masih terbuka.
Envelope, plan dan child ID deterministik; inventory yang sudah terjadwal tidak
digandakan. Crash setelah artifact write dapat menyisakan artefak tanpa child;
retry memakai ulang artefak tersebut dan storage kembali memeriksa authority.
Perintah ini tidak otomatis membatalkan target yang tertinggal. Setelah target
terpublikasi, gunakan replay `publish-graph`, bukan preparation pada parent baru.

Inventory dibatasi 256 plan dan input metadata sumber/kandidat 64 MiB, dengan
batas per-source/worker yang tetap berlaku. Angka ini batas implementasi, bukan
total byte I/O, karena validation/admission membaca ulang sebagian artefak, dan bukan
pengurangan workload benchmark. Ukur read/hash/SQL/queue latency, bytes, RSS dan
retry cost menurut [target required](../configs/benchmark-targets.yaml).
Quality/performance acceptance masih NOT_MEASURED. Uji executable memakai Rust,
PostgreSQL, Qdrant dan Neo4j nyata, tetapi label EXTRACT/review dan vektor sumber
sintetis. Lihat [laporan](verification-report-graph-prepare.md).

Pemilik kode: `workflows/graph_preparation.go` menyusun proses;
`adapters/postgres/graph_source_selection.go` membaca membership;
`cmd/cli/prepare_graph.go` memiliki konfigurasi/lifecycle dan memanggil admission
penjadwalan yang sudah ada. Incremental graph, mutasi canonical dan acceptance
corpus tetap pekerjaan terpisah.
