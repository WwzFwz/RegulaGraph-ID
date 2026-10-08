# Query evidence pada indeks terpublikasi

Dokumen ini menjelaskan perintah operator `query-evidence`, pemilihan route dari
katalog, dan batas verifikasinya. Perintah menjalankan retrieval/hidrasi produksi
tanpa generation jawaban. Ia memungkinkan pemeriksaan bukti sebelum tokenizer
prompt generator dan antarmuka jawaban disambungkan.

## Prasyarat dan penggunaan

Corpus harus mempunyai snapshot PUBLISHED dengan receipt dan katalog
generation/point yang valid, serta plan INDEX, source batch, normalized text dan
artefak lexical yang terdaftar. PDF yang selesai diunduh saja belum memenuhi
prasyarat. Perintah tidak menjalankan ingestion, migrasi, bootstrap collection,
atau menerbitkan snapshot. Native inference harus sudah memuat model embedding
persis seperti manifest generation.

Environment berikut dibaca CLI; file `.env` tidak dimuat otomatis:

| Variabel | Fungsi |
| --- | --- |
| `REGULAGRAPH_POSTGRES_DSN` | Database katalog/lease milik operator tepercaya |
| `REGULAGRAPH_ARTIFACTS_DIR` | Root FileStore yang sama dengan writer artefak terdaftar |
| `REGULAGRAPH_QUERY_CORPUS_ID` | Corpus yang diizinkan pada invocation operator ini |
| `REGULAGRAPH_BUILD_ID` | Revision/build pada producer retrieval |
| `REGULAGRAPH_QUERY_NATIVE_ENDPOINT` | IP loopback literal dan port, misalnya `127.0.0.1:50053` |
| `REGULAGRAPH_QDRANT_URL` | Origin yang sama persis dengan endpoint katalog, tanpa path/trailing slash |
| `REGULAGRAPH_QDRANT_API_KEY` | Credential origin tersebut; boleh kosong pada layanan lokal tanpa auth |
| `REGULAGRAPH_QUERY_RERANK_MANIFEST` | Opsional: path manifest model RERANK ProtoJSON C01, maksimum 64 KiB |
| `REGULAGRAPH_QUERY_RERANK_MANIFEST_SHA256` | SHA-256 lowercase atas byte manifest; wajib bersama path |

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

`-question`, `-as-of` dan `-profile` wajib. Profil hanya `vector` atau `hybrid`;
kegagalan BM25 tidak diganti dengan vector. Tanggal wajib YYYY-MM-DD.
`-snapshot` opsional harus sama dengan snapshot aktif yang dipin; historical
snapshot lain belum didukung. `-unresolved` default `report`, dengan pilihan
`exclude` atau `review`. Candidate limit berlaku per branch, 1..128; total
kapasitas hidrasi mencakup kedua branch. Timeout mencakup startup sampai hasil,
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
dan pastikan proses native yang sama memuat model embedding serta reranker tepat.
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
