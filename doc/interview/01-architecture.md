# Arsitektur: masalah, komponen, dan batas kesiapan

Dokumen ini menjadi gambaran besar RegulaGraph-ID untuk interview. Ia membedakan
arsitektur target Hybrid GraphRAG, komponen yang tersedia, dan demo BM25 yang aktif.
Peta implementasi rinci berada di [dokumen 5](05-code-map.md).

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
struktur relasi untuk mencari bukti. Dalam proyek ini, “Hybrid GraphRAG” ditargetkan
menggabungkan BM25, dense retrieval, dan pencarian melalui graph, lalu reranking.
Istilah itu tidak berarti proyek sudah mengimplementasikan setiap metode komunitas
atau community-summary dari implementasi GraphRAG lain.

## 2. Dua jalur utama arsitektur target

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

QUERY / SERVING TARGET
Pertanyaan -> Go admission + snapshot pin + query preparation
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

Diagram menunjukkan desain target, bukan daftar proses yang seluruhnya aktif.
`bind` adalah pekerjaan Go terhadap registry; tahap itu ditampilkan dalam rangkaian
transformasi dokumen agar dependency terlihat. Engine PDF dan runtime model berada
di bawah worker/client terkait, tidak ditulis ulang dari nol.

Ingestion dilakukan sebelum query agar pengguna tidak menunggu seluruh PDF diparse
atau graph dibangun ulang setiap kali bertanya. Query hanya mengolah pertanyaan,
kandidat, konteks terpilih, dan generation.

## 3. Bagian dalam setiap komponen

| Komponen | Isi dan tanggung jawab | Input → output | Status |
| --- | --- | --- | --- |
| Acquisition | Discovery, download, receipt, hash, deduplikasi byte, audit | URL → PDF/HTML + metadata sumber | KOMPONEN; dipakai menyiapkan data demo |
| Go control plane | Job, lease, checkpoint, retry, BIND, registry, publication | Request/job → transisi durable dan artefak terdaftar | KOMPONEN; coordinator semua tahap belum lengkap |
| Rust document worker | PDFium, normalisasi, mapping UTF-8, hierarchy, structural chunking, transform versi | Source refs → DocumentBatch | KOMPONEN; OCR/tabel dan validasi kualitas belum lengkap |
| Graph engineering | Ontology, mention, assertion, support, candidates, proposal resolution, assembly | Chunk berbukti → relasi/canonical assignments/GraphDelta | Sebagian KOMPONEN; graph penuh RENCANA |
| Indexing | Render teks, dense vectors, BM25 dictionary/statistics, batch admission | Chunk → IndexBatch + generation | KOMPONEN; coordinator corpus nyata masih perlu disambung |
| Native inference | Tokenizer, ONNX session, embedding, cross-encoder, batching/cancellation | Batch teks/pasangan → vector/skor | KOMPONEN; bukan generator demo |
| Retrieval Go | Query representation, lexical/dense branches, fusion, hydration, reranking | Pertanyaan + snapshot → kandidat/bukti | KOMPONEN untuk vector/hybrid; graph serving belum aktif |
| Answering Go | Context packing, model adapter, claim/citation mapping, validation | Bukti → draft/abstention | KOMPONEN; streaming/acceptance menyeluruh belum selesai |
| Evaluation Python | Dataset schema, metric, telemetry, gate runner | Output run + gold + manifest → laporan | KOMPONEN; gold lengkap dan acceptance belum dilakukan |

## 4. Pemisahan penyimpanan

| Penyimpanan | Mengapa ada | Bukan tanggung jawabnya |
| --- | --- | --- |
| PostgreSQL | Otoritas metadata, canonical registry, job/checkpoint, publication, read lease | Menjadi satu-satunya mesin komputasi embedding |
| Qdrant | Pencarian dense/sparse dan payload filtering pada generation yang kompatibel | Menentukan sendiri kebenaran versi hukum |
| Neo4j | Target penyimpanan adjacency dan jalur relasi beserta support | Membuat relasi benar hanya karena sudah menjadi edge |
| Blob/artifact storage | Byte PDF/teks/batch immutable, hash, replay, sumber kutipan | Menganggap checksum sebagai tanda tangan penerbit |

PostgreSQL menjadi sumber otoritas publication; pointer snapshot baru tidak boleh
aktif sebelum backend wajib siap. Kita tidak mengasumsikan transaksi atomik antara
PostgreSQL, Qdrant, dan Neo4j. Lihat [konsistensi storage](../storage-consistency.md).

## 5. Arsitektur demo yang benar-benar dijalankan

```text
PDF D01 -> Python offline / PyMuPDF -> teks halaman + SHA256SUMS
                                           |
                                    Go startup: verify + index
                                           |
Browser -> Go HTTP -> BM25 in-memory -> 5 kutipan -> Ollama / Qwen lokal
                                           |                |
                                  halaman + PDF       klaim + source IDs
                                           +------- validasi -------+
                                                        |
                                                   tampilan jawaban
```

Demo tidak memakai Qdrant, Neo4j, embedding C++, atau structural chunker Rust dalam
jalur pertanyaannya. Ia memakai kembali analyzer lexical Go dan adapter structured
generation. Potongan demo berupa window halaman. Sumber ditautkan ke halaman,
bukan hasil resolusi versi pasal yang sudah disahkan.

Sample yang diuji mempunyai 24 dokumen, 1.061 halaman, dan 2.071 passage.
Angka itu menjelaskan satu demo, bukan seluruh corpus. Jawaban berstatus
`unreviewed_draft`. Detail dan bukti: [panduan demo](../interview-demo.md) dan
[laporan](../verification-report-interview-demo.md).

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
