# Peta folder, file, dan tanggung jawab

Dokumen ini menghubungkan arsitektur dengan lokasi implementasi yang dapat dibuka
saat interview. Tree di bawah adalah peta fungsional terpilih, bukan daftar seluruh
file/test. Nama file menunjukkan lokasi; label status menjelaskan apakah perilaku
sudah aktif. Tidak ada pemindahan struktur repository melalui dokumen ini.

## 1. Peta root

```text
RegulaGraph-ID/
  src/                         Kode produk dan kontrak runtime
    server/                    Go: entry point, domain, workflow, retrieval, adapter
    ingestion/                 Rust: transformasi dokumen/graph/index batch
    inference/                 C++: tokenizer bridge, ONNX session, batching, RPC
    contracts/                 Protobuf/schema lintas bahasa + compatibility lock
  configs/                     Parameter, ontology, prompt dan benchmark targets
  migrations/                  Evolusi schema PostgreSQL
  evaluation/                  Dataset contract, metrics, runner dan gates Python
  tooling/                     Persiapan corpus/model dan eksperimen offline
  scripts/                     Entrypoint operasional tipis: build, check, start demo
  tests/                       Fixtures bersama, unit/integration lintas komponen
  deployment/                  Rencana packaging/operasi; deployment di luar scope kini
  data/                        Sumber PDF/HTML dan receipt akuisisi; data besar lokal
  artifacts/                   Output proses, model, manifest, log dan hasil eksperimen
  doc/                         Desain, keputusan, laporan verifikasi dan bahan interview
    interview/                 Panduan ini, bukan implementasi sistem kedua
```

Mengapa `data` dan `evaluation/datasets` berbeda? `data` menampung corpus sumber;
dataset evaluasi mendefinisikan pertanyaan, gold, split dan label untuk mengukur
sistem. PDF tersedia tidak berarti jawaban gold sudah dilabeli manusia.

## 2. Jalur demo: urutan file yang paling mudah ditunjukkan

| Urutan | File | Isi yang dapat dijelaskan |
| --- | --- | --- |
| 1 | [scripts/start_demo.ps1](../../scripts/start_demo.ps1) | Setup/build lokal, profil Ollama, startup/reuse; mode berbeda tidak diam-diam dipakai |
| 2 | [tooling/corpus/prepare_demo.py](../../tooling/corpus/prepare_demo.py) | Python offline: pilih PDF, periksa receipt/hash, ekspor text layer per halaman dan SHA256SUMS |
| 3 | [cmd/cli/demo.go](../../src/server/cmd/cli/demo.go) | Composition root Go: muat data, index, generator, handler, listener dan shutdown |
| 4 | [adapters/storage/preview.go](../../src/server/internal/adapters/storage/preview.go) | Verifikasi manifest, path confinement, batas file/bytes dan metadata sumber |
| 5 | [retrieval/preview.go](../../src/server/internal/retrieval/preview.go) | Passage window, postings, TF/IDF, BM25, ranking dan seleksi halaman |
| 6 | [workflows/preview.go](../../src/server/internal/workflows/preview.go) | Retrieval → opsional generation; satu slot model, status gagal/abstain |
| 7 | [answering/preview.go](../../src/server/internal/answering/preview.go) | Prompt, schema output, batas klaim dan pengecekan source IDs |
| 8 | [adapters/inference/llm.go](../../src/server/internal/adapters/inference/llm.go) | HTTP structured-output adapter; timeout, batas respons, model/status/accounting checks |
| 9 | [api/preview.go](../../src/server/internal/api/preview.go) | HTTP input/origin/host/deadline dan endpoint PDF terverifikasi |
| 10 | [api/preview.html](../../src/server/internal/api/preview.html) | UI, POST evidence lebih dahulu, lalu generation, render teks dan citation |
| 11 | [domain/local_preview.go](../../src/server/internal/domain/local_preview.go) | Nilai internal/UI halaman, passage, klaim dan jawaban; bukan kontrak worker baru |
| 12 | [configs/demo.Modelfile](../../configs/demo.Modelfile) | Profil Qwen lokal khusus demo; bukan pilihan model release yang sudah tervalidasi |

Semuanya **DEMO**. Lokasi library produksi di bawah tetap penting untuk menjelaskan
arsitektur, tetapi jangan mengatakan semuanya dipanggil oleh tombol demo.

## 3. Go: alur produksi dan pemilik keputusan

```text
src/server/
  cmd/
    cli/                       collect, audit, submit, query-evidence, demo
    ingestion-worker/          Coordinator durable dokumen/proposal resolution
    semantic-gateway/          EXTRACT/RESOLVE model gateway
    api/                       Entrypoint HTTP produksi masih scaffold
  gen/regulagraph/v1/           Generated Protobuf; diubah lewat schema/codegen
  internal/
    domain/                    Identitas, invariant, admission dan kontrak internal
    workflows/                 Urutan tahap, dependency, retry, cancellation
    ingestion/sources/         Discovery/download/receipt/audit portal
    indexing/                  Admission batch, backend write, publication
    retrieval/
      query/                   Analyzer, normalizer, query artifacts, alias linking
      graph/                   Target traversal dan path evidence
    answering/                 Context, generation, claim/citation validation
    adapters/
      postgres/                Registry, job, checkpoint, catalog, publication
      qdrant/                  Payload, collection, search, upsert/readback
      neo4j/                   Target persistence graph; masih scaffold
      storage/                 Immutable artifacts, hash dan path confinement
      inference/               Native model client dan structured provider adapter
      worker/                  Client transport ke Rust worker
    api/                       HTTP translation; tidak memiliki algoritma retrieval
```

| Bagian | File yang dibuka | Status dan pekerjaan lanjutan |
| --- | --- | --- |
| Job → dokumen | [parse.go](../../src/server/internal/workflows/parse.go), [bind.go](../../src/server/internal/workflows/bind.go) | KOMPONEN: dispatch/checkpoint dokumen dan binding; lanjutkan tahap sesudahnya |
| Proposal resolution | [semantic_resolution_executor.go](../../src/server/internal/workflows/semantic_resolution_executor.go), [semantic_resolution_model.go](../../src/server/internal/workflows/semantic_resolution_model.go) | KOMPONEN: proposal model dan handoff; review/resume lengkap belum aktif |
| Otoritas identity | [registry_aliases.go](../../src/server/internal/adapters/postgres/registry_aliases.go), [registry_semantic.go](../../src/server/internal/adapters/postgres/registry_semantic.go) | KOMPONEN: alias/revision dan keputusan registry, bukan fuzzy nama tanpa scope |
| Index admission/write | [initial_prepare.go](../../src/server/internal/indexing/initial_prepare.go), [initial_writer.go](../../src/server/internal/indexing/initial_writer.go) | KOMPONEN: writer snapshot awal dengan batas khusus; coordinator corpus nyata belum lengkap |
| Publication | [publication.go](../../src/server/internal/indexing/publication.go), [PG publication.go](../../src/server/internal/adapters/postgres/publication.go) | KOMPONEN: reserve/stage/readiness/CAS; seluruh backend flow belum dirangkai |
| Katalog indeks | [index_catalog.go](../../src/server/internal/adapters/postgres/index_catalog.go), [migration 0014](../../migrations/0014_index_catalog.up.sql) | KOMPONEN: catalog binding dan authoritative record lookup |
| Satu snapshot per request | [rag_session.go](../../src/server/internal/workflows/rag_session.go) | KOMPONEN: lease/pin selama search dan answer; profile/as-of dibatasi eksplisit |
| Factory backend query | [published_query.go](../../src/server/internal/workflows/published_query.go) | KOMPONEN: katalog memilih collection/generation, bukan nama tebakan dari user |
| Parallel dense/lexical | [retrieval.go](../../src/server/internal/workflows/retrieval.go), [dense.go](../../src/server/internal/retrieval/dense.go), [lexical.go](../../src/server/internal/retrieval/lexical.go) | KOMPONEN: Vector/Hybrid RAG, belum graph branch |
| Query representation | [lexical.go](../../src/server/internal/retrieval/query/lexical.go), [sparse.go](../../src/server/internal/retrieval/query/sparse.go), [entity_linker.go](../../src/server/internal/retrieval/query/entity_linker.go) | Analyzer/sparse KOMPONEN; entity_linker masih scaffold |
| Fusion/rerank | [fusion.go](../../src/server/internal/retrieval/fusion.go), [reranking.go](../../src/server/internal/retrieval/reranking.go) | KOMPONEN: RRF serta korelasi/urutan skor; helper tidak membuktikan seluruh wiring rerank |
| Source hydration | [hydration.go](../../src/server/internal/retrieval/hydration.go), [rag_hydration.go](../../src/server/internal/workflows/rag_hydration.go) | KOMPONEN: byte/metadata otoritatif, temporal policy dan accounting |
| Context dan jawaban | [context_builder.go](../../src/server/internal/answering/context_builder.go), [generator.go](../../src/server/internal/answering/generator.go), [citations.go](../../src/server/internal/answering/citations.go), [validation.go](../../src/server/internal/answering/validation.go) | KOMPONEN: packing/draft/structural checks; parent/path completion, streaming dan gold masih terbuka |
| Qdrant transport | [collections.go](../../src/server/internal/adapters/qdrant/collections.go), [search.go](../../src/server/internal/adapters/qdrant/search.go) | KOMPONEN: writer bootstrap versus reader admission, search/filter; tidak dipakai demo |
| Neo4j/graph search | [store.go](../../src/server/internal/adapters/neo4j/store.go), [traversal.go](../../src/server/internal/retrieval/graph/traversal.go) | RENCANA/scaffold; implementasi mutation/traversal/versioned support masih diperlukan |

Catatan integrasi terbuka: investigasi sebelum demo menemukan asumsi ID artefak
fisik sama dengan record ID logis pada initial_prepare/hydration. Hasil worker
content-addressed perlu diakomodasi dengan benar sebelum mengklaim jalur Rust
nyata → publication → query selesai. Lihat [laporan demo](../verification-report-interview-demo.md).

## 4. Rust: komputasi persiapan data

| Bagian | Lokasi | Isi/status |
| --- | --- | --- |
| Parsing | [pdf.rs](../../src/ingestion/src/document/parsing/pdf.rs), [ocr.rs](../../src/ingestion/src/document/parsing/ocr.rs) | PDFium KOMPONEN; OCR masih scaffold |
| Normalisasi | [text.rs](../../src/ingestion/src/document/normalization/text.rs) | KOMPONEN: teks dan mapping, jangan kehilangan offset asli |
| Structural chunking | [structural.rs](../../src/ingestion/src/document/chunking/structural.rs), [builder.rs](../../src/ingestion/src/document/chunking/builder.rs), [tokenizer.rs](../../src/ingestion/src/document/chunking/tokenizer.rs) | KOMPONEN: struktur/tokenizer terpin; evaluasi gold terpisah |
| Versi dan perubahan | [provisions.rs](../../src/ingestion/src/document/versioning/provisions.rs), [change_detection.rs](../../src/ingestion/src/document/change_detection.rs) | KOMPONEN/library; extraction change-event dan equivalence end-to-end belum selesai |
| Extraction | [extractor.rs](../../src/ingestion/src/knowledge_graph/extraction/extractor.rs) | KOMPONEN: hasil model menjadi proposal berbukti |
| Resolution | [blocking.rs](../../src/ingestion/src/knowledge_graph/resolution/blocking.rs), [resolver.rs](../../src/ingestion/src/knowledge_graph/resolution/resolver.rs) | KOMPONEN: kandidat dan proposal; PostgreSQL tetap otoritas keputusan |
| Assembly | [builder.rs](../../src/ingestion/src/knowledge_graph/assembly/builder.rs) | Library materialisasi endpoint tersedia; bukan seluruh graph publication Neo4j |
| Dense/sparse index | [build.rs](../../src/ingestion/src/indexing/build.rs), [inputs.rs](../../src/ingestion/src/indexing/inputs.rs), [dense.rs](../../src/ingestion/src/indexing/dense.rs), [lexical.rs](../../src/ingestion/src/indexing/lexical.rs), [statistics.rs](../../src/ingestion/src/indexing/statistics.rs) | KOMPONEN: prepared input, vector dan frozen BM25; worker bukan pemilik commit backend |
| Worker INDEX | [index.rs](../../src/ingestion/src/worker/index.rs) | KOMPONEN: menerima plan/manifest dan mengembalikan artefak batch; coordinator plan/dispatch corpus masih perlu disambung |

## 5. C++ dan model tooling

| File | Peran |
| --- | --- |
| [main.cpp](../../src/inference/src/main.cpp) | Bootstrap layanan native; lifecycle/config eksplisit |
| [runtime.cpp](../../src/inference/src/runtime.cpp) | Session model dan backend ONNX yang dipakai ulang |
| [model_integrity.cpp](../../src/inference/src/model_integrity.cpp) | Admission model bundle/hash sebelum dimuat |
| [embeddings.cpp](../../src/inference/src/embeddings.cpp) | Embedding, pooling/normalisasi sesuai manifest |
| [cross_encoder.cpp](../../src/inference/src/cross_encoder.cpp) | Pasangan query–passage menjadi skor reranking |
| [batching.cpp](../../src/inference/src/batching.cpp) | Antrean/batch berbatas, priority/cancellation dan accounting |
| [service.cpp](../../src/inference/src/service.cpp) | Boundary layanan batch inference |
| [tooling/models/export.py](../../tooling/models/export.py) | Ekspor/model preparation offline; bandingkan native dengan reference |

Status semuanya KOMPONEN, dengan batas bukti pada [native-inference](../native-inference.md).
Generator demo menggunakan Ollama, tidak `cross_encoder.cpp` atau `embeddings.cpp`.

## 6. Kontrak, konfigurasi, evaluasi

| Lokasi | Penjelasan untuk interview |
| --- | --- |
| [src/contracts/proto](../../src/contracts/proto/README.md) | Satu sumber wire schema untuk worker/inference/evidence/jobs; binding generated tidak diedit manual |
| [schema-lock.json](../../src/contracts/schema-lock.json), [check_contracts.py](../../scripts/check_contracts.py) | Compatibility baseline dan pemeriksaan; bukan izin mengubah lock untuk meluluskan tes |
| [configs](../../configs/README.md) | Ontology, prompt, policy dan target; rahasia tetap di environment |
| [evaluation/runner.py](../../evaluation/runner.py) | Menilai run/gates dengan manifest dan eligibility yang sesuai |
| [evaluation/metrics](../../evaluation/metrics/README.md) | Retrieval, graph, answer, citation, runtime dipisah agar sumber masalah terlihat |
| [evaluation/datasets](../../evaluation/datasets/README.md) | Kontrak gold dan split; anotasi manusia lengkap belum selesai |
| [tests](../../tests/README.md) | Invariant dan regression tests; fixture PASS bukan model-quality PASS |

Saat menunjuk suatu test, jelaskan failure yang dicegah: citation palsu, snapshot
bercampur, skor reranker tertukar, artifact korup, atau retry menggandakan mutation.
“Jumlah test banyak” saja bukan penjelasan kualitas engineering.
