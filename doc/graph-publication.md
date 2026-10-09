# Publication graph melalui CLI

Dokumen ini menjelaskan `publish-graph`, penghubung inventory ASSEMBLE yang sudah
selesai ke snapshot GraphRAG yang dapat ditanya. Go melakukan admission, write,
receipt dan aktivasi; Rust sudah menghasilkan GraphDelta sebelumnya. Perintah ini
tidak memilih canonical entity, menyetujui resolution, atau memanggil model.

## Prasyarat dan input

Terapkan migration sampai 0023 serta pendahulunya. Corpus harus mempunyai indeks
dense/BM25 terbit, reservation graph dengan parent indeks tersebut, registry
binding, source receipts, inventory ASSEMBLE lengkap, dan output seluruh child
STAGED. [Preparation](graph-publication-preparation.md) serta
[daemon ASSEMBLE](graph-job-execution.md) menjelaskan dependency tersebut.
[prepare-graph](graph-preparation.md) menyediakan preparation/scheduling inventory
dari seluruh sumber indeks yang siap. Command publication ini dijalankan setelah
worker menyelesaikan inventory tersebut.

Gunakan artifact root yang sama dengan worker. Pilih route eksplisit dan ontology
byte-pinned yang sama dengan extraction/assembly:

```powershell
$env:REGULAGRAPH_POSTGRES_DSN = '<local DSN>'
$env:REGULAGRAPH_ARTIFACTS_DIR = '<shared artifact root>'
$env:REGULAGRAPH_NEO4J_URI = 'bolt://127.0.0.1:7687'
$env:REGULAGRAPH_NEO4J_DATABASE = 'neo4j'
$env:REGULAGRAPH_NEO4J_USERNAME = 'neo4j'
$env:REGULAGRAPH_NEO4J_PASSWORD = '<local credential>'
$env:REGULAGRAPH_QDRANT_URL = 'http://127.0.0.1:6333'
$env:REGULAGRAPH_ONTOLOGY_PATH = (Resolve-Path configs/ontology-v1.jsonc).Path
$env:REGULAGRAPH_ONTOLOGY_SHA256 = '<approved SHA256 of those bytes>'
go run ./src/server/cmd/cli publish-graph `
  -corpus corpus:example -publication publication:graph-example `
  -snapshot snapshot:graph-example -generation graph-generation:example `
  -auth-scope operator:corpus -timeout 5m
```

Identitas contoh harus diganti dengan reservation/inventory aktual. Generation
adalah namespace fisik Neo4j immutable. Endpoint Qdrant harus sama persis dengan
catalog; perintah tidak memindahkan indeks. Qdrant API key opsional melalui
`REGULAGRAPH_QDRANT_API_KEY`. Nilai credential tidak disimpan dalam output/log.
Environment tidak dibaca otomatis dari `.env`.

## Proses dan output

CLI memeriksa inventory corpus/scope/ontology, reservation/fence/sequence, lalu
mem-pin parent aktif. Shared `ReadGraphInventoryInputs` mengembalikan byte sumber
yang sama dengan cold restore daemon, dengan total input 64 MiB. PostgreSQL
membangun ulang admission; `PrepareCompletedGraph` memeriksa semua checkpoint,
hash, keputusan registry dan projection sumber sebelum remote mutation.

`PublishPreparedGraph` menghitung graph catalog dengan projection produksi dan
merakit manifest berisi Neo4j serta Qdrant origin yang diwarisi. Parent, generation,
model dan sumber asli tetap terikat. ManifestHash mengikuti deskripsi graph yang
mencakup snapshot parent; backend count/checksum berasal dari artefak, bukan angka
yang dimasukkan operator. ValidationReport menyatakan pemeriksaan struktural;
tidak berarti kualitas semantik atau benchmark lulus.

Setelah stage, writer mereservasi catalog/write intent, menulis dan memverifikasi
Neo4j exact, lalu menyimpan graph receipt. Reuse Qdrant membaca seluruh catalog,
memeriksa payload/vector/count dan visibility target tanpa embedding/upsert.
Receipt kedua diikuti publication guard serta CAS active pointer. Query lama
tetap memiliki snapshot/pin miliknya. Graph yang tertulis tetapi Qdrant gagal
belum menjadi snapshot aktif.

Exit 0 mengeluarkan `{ "status": "published", "snapshot": <C01 SnapshotRef> }`.
Exit 2 berarti invocation/config invalid; exit 1 berarti publication gagal atau
output tidak valid. Sukses menyatakan snapshot pernah dipublikasikan, bukan
jaminan bahwa tidak ada publisher berikutnya atau bahwa semua backend masih sehat.
Pakai readiness/query untuk keadaan serving saat ini.

## Retry, batas dan verifikasi

Retry harus memakai argument, route, ontology, inventory dan bytes yang sama.
Lost acknowledgement meninggalkan intent/receipt untuk diperiksa ulang. Replay
snapshot PUBLISHED membaca durable manifest/catalog/source origin dan menolak
scope/generation/route drift; ia tidak menulis ulang atau menjalankan inference.
Tidak ada rollback atomik lintas Neo4j/Qdrant/PostgreSQL. Cleanup artefak/backend
orphan dan takeover publication termasuk pekerjaan recovery/GC berikutnya.

Timeout total 1 detik–30 menit; call backend dibatasi 30 detik. Pin dilepas dengan
bounded cleanup dan memiliki expiry. Batas tersebut bukan perubahan target
`configs/benchmark-targets.yaml`. Alur ini menambah graph dengan source membership
indeks yang tetap; ia tidak merealisasikan full incremental closure atau canonical
merge/split. Bukti fixture native dan keterbatasan kualitas berada pada
[laporan](verification-report-graph-publish.md).
