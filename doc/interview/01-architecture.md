# Arsitektur lengkap RegulaGraph-ID

Dokumen ini menjelaskan RegulaGraph-ID dengan asumsi semua komponen arsitektur
sudah terintegrasi. Fokusnya adalah cara kerja sistem lengkap dan tanggung jawab
setiap bagian. Peta kode ada di [dokumen 5](05-code-map.md); keadaan repository
sebenarnya dipisahkan di [status implementasi](07-implementation-status.md).

Untuk input, proses, output dan contoh konkret **setiap tahap**, baca
[perjalanan data end-to-end](08-input-output-examples.md). Satu contoh regulasi
fiktif diikuti dari unduhan PDF, struktur pasal, graph/index, query sampai jawaban.

## 1. Masalah yang ingin diselesaikan

Regulasi tersebar pada portal berbeda, saling merujuk, dan dapat berubah sebagian.
Pertanyaan pengguna dapat membutuhkan definisi pada satu pasal, prosedur pada
pasal lain, serta pengecualian atau perubahan dari dokumen berbeda. Pencarian satu
paragraf yang mirip belum tentu menemukan semua bukti yang diperlukan.

Tujuan sistem adalah membantu menemukan dan menjelaskan bukti regulasi dengan
identitas sumber, lokasi teks, versi, serta hubungan yang dapat diperiksa. Model
bahasa membantu interpretasi dan penyusunan jawaban; database dan kontrak menjaga
identitas serta provenance. Output fasih tidak otomatis dianggap benar.

RAG berarti jawaban dibangun dari hasil retrieval. GraphRAG menambahkan penggunaan
struktur relasi untuk mencari bukti. Dalam proyek ini, “Hybrid GraphRAG” menggabungkan BM25, dense retrieval, dan pencarian melalui graph, lalu reranking.
Graph yang dimaksud adalah graph regulasi berbukti; community-summary dari
pendekatan GraphRAG lain bukan syarat definisi arsitektur ini.

## 2. Dua jalur utama arsitektur lengkap

```text
PERSIAPAN DATA / INGESTION
Portal resmi -> Go acquisition + scheduler -> Rust document processing
                                               |
                         parse -> normalize -> structure -> bind -> chunk
                                               |
                             model extraction/resolution + validasi
                                               |
                                  graph delta + index batch
                                               |
                               Go storage/publication coordinator
                          /             |              |            \
                    PostgreSQL       Qdrant          Neo4j       blob storage

QUERY / SERVING
Pertanyaan -> Go admission + snapshot pin + normalize/classify/plan
                         |
               +---------+------------------+
               |         |                  |
              BM25   embedding -> dense   graph retrieval
               +---------+------------------+
                         |
                 filter + fusion -> rerank
                         |
          source/parent/path hydration -> context -> LLM generation
                         |
                 validasi klaim/sitasi -> jawaban
```

Diagram mengikuti asumsi integrasi lengkap.
`bind` adalah pekerjaan Go terhadap registry; tahap itu ditampilkan dalam rangkaian
transformasi dokumen agar dependency terlihat. Engine PDF dan runtime model berada
di bawah worker/client terkait, tidak ditulis ulang dari nol.

Ingestion dilakukan sebelum query agar pengguna tidak menunggu seluruh PDF diparse
atau graph dibangun ulang setiap kali bertanya. Query hanya mengolah pertanyaan,
kandidat, konteks terpilih, dan generation.

Diagram ini memadatkan routing dan feedback loop. Classifier memilih
kebutuhan factual/relational/temporal yang dapat tumpang tindih; evidence yang belum
memadai dapat memicu pencarian tambahan dalam deadline/budget yang sama. Temporal
policy berlaku lintas branch. Lihat [alur adaptif](03-flows.md), termasuk pemeriksaan
bukti sebelum generation dan validasi klaim sesudahnya.

## 3. Bagian dalam setiap komponen

| Komponen | Isi dan tanggung jawab | Input → output |
| --- | --- | --- |
| Acquisition | Discovery, download, receipt, hash, deduplikasi byte, audit | URL → PDF/HTML + metadata sumber |
| Go control plane | Job, lease, checkpoint, retry, BIND, registry, publication | Request/job → transisi durable dan artefak terdaftar |
| Rust document worker | PDFium, normalisasi, mapping UTF-8, hierarchy, structural chunking, transform versi | Source refs → DocumentBatch |
| Graph engineering | Ontology, mention, assertion, support, candidates, proposal resolution, assembly | Chunk berbukti → relasi/canonical assignments/GraphDelta |
| Indexing | Render teks, dense vectors, BM25 dictionary/statistics, batch admission | Chunk → IndexBatch + generation |
| Native inference | Tokenizer, ONNX session, embedding, cross-encoder, batching/cancellation | Batch teks/pasangan → vector/skor |
| Retrieval Go | Query representation, lexical/dense/graph branches, fusion, hydration, reranking | Pertanyaan + snapshot → kandidat/bukti |
| Answering Go | Context packing, model adapter, claim/citation mapping, validation | Bukti → draft/abstention |
| Evaluation Python | Dataset schema, metric, telemetry, gate runner | Output run + gold + manifest → laporan |

## 4. Pemisahan penyimpanan

| Penyimpanan | Mengapa ada | Bukan tanggung jawabnya |
| --- | --- | --- |
| PostgreSQL | Otoritas metadata, canonical registry, job/checkpoint, publication, read lease | Menjadi satu-satunya mesin komputasi embedding |
| Qdrant | Pencarian dense/sparse dan payload filtering pada generation yang kompatibel | Menentukan sendiri kebenaran versi hukum |
| Neo4j | Penyimpanan adjacency dan jalur relasi beserta support | Membuat relasi benar hanya karena sudah menjadi edge |
| Blob/artifact storage | Byte PDF/teks/batch immutable, hash, replay, sumber kutipan | Menganggap checksum sebagai tanda tangan penerbit |

PostgreSQL menjadi sumber otoritas publication; pointer snapshot baru tidak boleh
aktif sebelum backend wajib siap. Kita tidak mengasumsikan transaksi atomik antara
PostgreSQL, Qdrant, dan Neo4j. Lihat [konsistensi storage](../storage-consistency.md).

## 5. Kendali kualitas dan pencarian adaptif

Classifier memetakan pertanyaan menjadi kebutuhan retrieval. Factual mengutamakan
pencocokan lexical/semantik; relational membutuhkan hubungan dan path support;
version-aware menambahkan batas tanggal pada seluruh branch yang relevan. Ketiga
kebutuhan ini dapat hadir bersamaan.

Setelah fusion, reranking dan context hydration, sistem memeriksa kecukupan bukti.
Dependency yang belum terpenuhi memicu retrieval terarah: mencari parent, pasal
rujukan, exception, atau versi yang sesuai. Semua putaran menggunakan snapshot yang
sama dan mengonsumsi budget total yang sama. Bila tidak ada kemajuan atau budget
habis, keluaran menjadi partial/abstain atau meminta klarifikasi.

Generation dilakukan dari konteks yang dipilih, kemudian klaim dan citation
diperiksa kembali. Kekurangan bukti dapat kembali ke retrieval; salah bentuk
output atau klaim yang tidak didukung dapat memerlukan revisi jawaban. Jalur ini
menghindari loop generate/retrieve tanpa batas. [Dokumen flow](03-flows.md)
menjelaskan setiap handoff.

## 6. Identitas yang tidak boleh tertukar

| Identitas | Artinya |
| --- | --- |
| Source/blob hash | Identitas byte file; dua mirror dapat memuat byte yang sama |
| Canonical entity ID | Identitas entitas yang ditetapkan registry, tidak berubah hanya karena alias |
| Provision-version ID | Versi tertentu dari pasal/ayat; bukan sekadar teks “Pasal 5” |
| Snapshot ID | Revisi pengetahuan corpus yang dibaca satu request |
| Representation generation | Model/analyzer/dictionary/statistics yang menghasilkan indeks |
| Artifact ID vs logical record ID | Alamat artefak tersimpan dan identitas record di dalamnya; keduanya tidak selalu sama |

Contoh konseptual: dua peraturan bernomor 5 dari penerbit berbeda tidak otomatis
sama. Nama yang mirip tidak membuktikan identitas. Tanggal pengunduhan juga tidak
menentukan tanggal mulai berlaku suatu pasal.
