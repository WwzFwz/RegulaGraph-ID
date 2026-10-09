# Query evidence pada indeks terpublikasi

Dokumen ini menjelaskan perintah operator `query-evidence`, pemilihan route dari
katalog, dan batas verifikasinya. Perintah menjalankan retrieval/hidrasi produksi
tanpa generation jawaban secara default. Mode eksplisit `-answer` menyambungkan
generator/tokenizer lokal terpin; lihat [panduan jawaban lokal](local-answer.md).

## Prasyarat dan penggunaan

Corpus harus mempunyai snapshot PUBLISHED dengan receipt dan katalog
generation/point yang valid, serta plan INDEX, source batch, normalized text dan
artefak lexical yang terdaftar. PDF yang selesai diunduh saja belum memenuhi
prasyarat. Perintah tidak menjalankan ingestion, migrasi, bootstrap collection,
atau menerbitkan snapshot. Profil dense memerlukan native inference yang memuat
embedding persis seperti manifest generation. Graph-only tanpa reranker tidak
memerlukan endpoint native, tetapi tetap membutuhkan indeks sumber Qdrant.

Environment berikut dibaca CLI; file `.env` tidak dimuat otomatis:

| Variabel | Fungsi |
| --- | --- |
| `REGULAGRAPH_POSTGRES_DSN` | Database katalog/lease milik operator tepercaya |
| `REGULAGRAPH_ARTIFACTS_DIR` | Root FileStore yang sama dengan writer artefak terdaftar |
| `REGULAGRAPH_QUERY_CORPUS_ID` | Corpus yang diizinkan pada invocation operator ini |
| `REGULAGRAPH_QUERY_AUTH_SCOPE` | Scope operator tepercaya yang sama dengan publication; default `operator:local-query` |
| `REGULAGRAPH_BUILD_ID` | Revision/build pada producer retrieval |
| `REGULAGRAPH_QUERY_NATIVE_ENDPOINT` | IP loopback literal dan port, misalnya `127.0.0.1:50053` |
| `REGULAGRAPH_QDRANT_URL` | Origin yang sama persis dengan endpoint katalog, tanpa path/trailing slash |
| `REGULAGRAPH_QDRANT_API_KEY` | Credential origin tersebut; boleh kosong pada layanan lokal tanpa auth |
| `REGULAGRAPH_QUERY_RERANK_MANIFEST` | Opsional: path manifest model RERANK ProtoJSON C01, maksimum 64 KiB |
| `REGULAGRAPH_QUERY_RERANK_MANIFEST_SHA256` | SHA-256 lowercase atas byte manifest; wajib bersama path |
| `REGULAGRAPH_QUERY_GRAPH_CONFIG` | File JSON graph schema 1, maksimal 64 KiB; wajib untuk profil graph |
| `REGULAGRAPH_QUERY_GRAPH_CONFIG_SHA256` | Hash byte file graph; dipin bersama path |
| `REGULAGRAPH_NEO4J_USERNAME`, `REGULAGRAPH_NEO4J_PASSWORD` | Credential graph; tidak disimpan dalam file config/hash |

Qdrant HTTP hanya diterima pada IP loopback literal; endpoint remote memakai
HTTPS. Native gRPC plaintext juga hanya loopback literal. CLI ini alat operator
dengan akses konfigurasi/DB, **bukan** authorization multi-user atau endpoint
HTTP publik. Jangan meneruskan environment/DSN operator ke request pengguna.
Endpoint/collection/generation berasal dari katalog, bukan argumen pertanyaan.
Rahasia tidak masuk output atau fingerprint konfigurasi.

Setelah prasyarat terpenuhi, jalankan dari root:

```powershell
go run ./src/server/cmd/cli query-evidence -question "Apa ketentuan perizinannya?" -as-of 2026-01-01 -profile hybrid -unresolved report -limit 20 -timeout 30s
```

`-question` dan `-profile` wajib; pilih `-as-of` atau `-current` dengan zona operator ([panduan](current-query.md)). Profil: `vector`, `hybrid`, `graph`,
atau `hybrid-graph`; cabang gagal tidak diganti dengan profil lain. Tanggal YYYY-MM-DD.
`-snapshot` opsional harus sama dengan snapshot aktif yang dipin; historical
snapshot lain belum didukung. `-unresolved` default `report`, dengan pilihan
`exclude` atau `review`. Candidate limit berlaku per branch, 1..128; khusus
`hybrid-graph` maksimal 85 agar ketiga branch muat dalam admission hidrasi 256.
Tidak ada pemotongan diam-diam untuk mencapai batas tersebut. Timeout mencakup startup sampai hasil,
maksimum lima menit; cleanup lease memakai konteks terpisah terbatas.

Stdout berupa satu JSON dengan `mode: "evidence"`, `evidence` dalam ProtoJSON
C01, dan `rejected` yang mencatat alasan penolakan kandidat. Evidence memuat
teks/spans, source/version refs, status, provenance ranking dan snapshot. Tidak
ada jawaban LLM. PARTIAL tetap PARTIAL. Exit 0 berarti query berhasil dengan
outcome eksplisit, bukan jaminan bukti lengkap atau relevan; exit 1 berarti
runtime/integrity/output gagal; exit 2 berarti argumen/config invalid. Error
backend disanitasi agar credential atau dump provider tidak ikut tercetak.

## Reranking native opsional

Tanpa kedua variabel reranker, query memakai urutan fusion. Untuk mengaktifkan
reranker, gunakan `model.pbjson` dari bundle model yang sudah diverifikasi sesuai
[panduan native](native-inference.md), bekukan hash byte manifest dalam konfigurasi,
dan pastikan proses native memuat reranker tepat, serta embedding jika profil
memakai dense. Graph-only dengan reranking tetap membutuhkan native.
CLI mencocokkan keduanya dengan capabilities runtime sebelum query. Hash tidak
boleh sekadar diperbarui otomatis ketika file model berubah.

Setelah hidrasi dan filter tanggal, semua bukti yang diterima dinilai ulang dalam
batch maksimum 32 pasangan dan 4 MiB protobuf, dengan deadline request yang sama.
Satu pasangan yang terlalu besar ditolak sebelum RPC pertama; respons model
hilang, terpotong atau tidak cocok menggagalkan query. Tidak ada pengurangan
bukti atau fallback tersembunyi. Skor sama mempertahankan urutan fusion lintas
batch. Source refs, spans, provenance cabang dan PARTIAL tetap dipertahankan.

Output menambahkan `reranking` dengan `model` dan `scores` ProtoJSON C01 dalam
urutan evidence, `batches`, dan `duration_ns`. Skor adalah relevansi model, bukan
probabilitas jawaban benar. Durasi mencakup tahap reranking di client, bukan
pengukuran terpisah antrean server. Model/policy batch masuk fingerprint query.
`RAGWorkflow` memakai tahap yang sama sebelum konteks/generation jika caller
memasang reranker. Benchmark sebelum/sesudah pada gold serta p95/p99 belum
diukur; [laporan](verification-report-evidence-reranking.md) membatasi bukti
yang sudah diuji.

## Konfigurasi graph terpin

Graph membutuhkan snapshot gabungan terpublikasi, registry alias yang sesuai,
dan generation Neo4j tersegel. Endpoint/database pada file harus persis cocok
dengan katalog sebelum credential dipakai. URI `bolt://` dibatasi loopback;
remote memakai `bolt+s://`. Preparation tidak membuat schema, graph atau collection.

Contoh bentuk file berikut **bukan pilihan scope untuk corpus produksi**. Ganti
corpus, namespace/type, route dan budget dengan konfigurasi registry yang dipakai
saat ingestion. Budget adalah batas kerja, bukan jaminan recall/latency.

```json
{
  "schema_version": 1,
  "corpus": "corpus:example",
  "endpoint": "bolt://127.0.0.1:7687",
  "database": "neo4j",
  "linking": {
    "namespaces": [{"entity_type": "organization", "scope": "ID:national"}],
    "maximum_query_bytes": 4096,
    "maximum_phrase_tokens": 6,
    "maximum_phrases": 256,
    "maximum_lookups": 1024,
    "maximum_aliases_per_lookup": 32,
    "maximum_seeds": 64
  },
  "traversal": {
    "maximum_hops": 3,
    "maximum_paths": 128,
    "assertions": 128,
    "supports": 256,
    "bytes": 1048576
  }
}
```

Setelah memeriksa isi file, pin hash byte persis secara sengaja:

```powershell
$env:REGULAGRAPH_QUERY_GRAPH_CONFIG = "C:\path\query-graph.json"
$env:REGULAGRAPH_QUERY_GRAPH_CONFIG_SHA256 = (Get-FileHash -LiteralPath $env:REGULAGRAPH_QUERY_GRAPH_CONFIG -Algorithm SHA256).Hash.ToLowerInvariant()
go run ./src/server/cmd/cli query-evidence -question "Apa hubungan Badan A?" -as-of 2026-01-01 -profile graph -limit 20 -timeout 30s
```

Variabel storage/corpus/build, scope serta credential pada tabel tetap wajib
sesuai profil. File `.env` tidak otomatis dibaca. Hash config dan policy linking
masuk producer; password tidak. Alias ambiguity/unreviewed/temporal tetap
menjadi missing dependency pada bukti. Lihat [linking](query-entity-linking.md).
CLI mengembalikan evidence, bukan generation LLM. Integrasi draft ada di workflow;
API graph configuration dan command answering masih pekerjaan tersendiri.

## Integrasi dan resource

`RAGSession.SearchQuestion` memakai admission/lease yang sama dengan answering.
`RAGWorkflow.SearchPinnedQuestion` menjalankan search, fusion dan accounting
hydration bersama; `AnswerPinnedQuestion` menambahkan answering setelah tahap
yang sama. Tidak ada algoritma retrieval kedua khusus CLI/evaluator.

`PreparePublishedQuery` memilih credential berdasarkan origin persis, memanggil
`OpenExistingCollection` yang hanya membaca layout Qdrant, lalu memuat BM25 dari
analyzer/dictionary/statistik yang cocok dengan registry dan hash. Dictionary
parent atau statistik base lama memerlukan checked ancestry dan belum didukung
factory snapshot awal. Model diperiksa melalui native capabilities sebelum
query embedding. Artefak bermasalah menggagalkan persiapan, bukan branch kosong.

`PreparedQuery.Bind` membuat hydrator untuk pin baru pada binding yang sama.
Route/model/generation berubah harus mempersiapkan resource set baru. Immutable
BM25 dan HTTP/native clients dapat dipakai ulang layanan berumur panjang;
cache antar-generation dan lifecycle API belum dibuat. CLI satu query per
invocation: pool DB, koneksi, layout admission dan lexical load adalah biaya
**cold setup** setiap invocation. Proses native yang memuat weights tetap
dipakai ulang; CLI tidak memuat model per request. Jangan melaporkan waktu CLI
sebagai latency warm server.

Batas unresolved date, parent/exception dan proyeksi teks versi campuran mengikuti
[kontrak hidrasi](pinned-evidence.md). Ukur startup, embedding/queue, DB/payload,
hydration, p95/p99 dan RSS terpisah; kualitas/recall tetap dinilai bersama.
Target required tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml),
REQUIRED_UNMEASURED. Bukti tes dan model sintetis dipisahkan dalam
[laporan verifikasi](verification-report-query-evidence.md).
