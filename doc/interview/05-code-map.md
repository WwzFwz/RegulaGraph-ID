# Peta folder, file, dan tanggung jawab

Dokumen ini memetakan tanggung jawab arsitektur lengkap ke folder dan file pemiliknya.
Seperti dokumen 01-06 lainnya, uraian fungsi memakai asumsi integrasi lengkap.
Lokasi file dapat dibuka sekarang, tetapi daftar tanggung jawab bukan pernyataan
bahwa semua isi file sudah selesai; status aktual ada di [dokumen 7](07-implementation-status.md).
Tree adalah peta terpilih, bukan daftar seluruh file/test atau perubahan struktur.

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
  deployment/                  Packaging dan konfigurasi operasi
  data/                        Sumber PDF/HTML dan receipt akuisisi; data besar lokal
  artifacts/                   Output proses, model, manifest, log dan hasil eksperimen
  doc/                         Desain, keputusan, laporan verifikasi dan bahan interview
    interview/                 Panduan ini, bukan implementasi sistem kedua
```

Mengapa `data` dan `evaluation/datasets` berbeda? `data` menampung corpus sumber;
dataset evaluasi mendefinisikan pertanyaan, gold, split dan label untuk mengukur
sistem. PDF tersedia tidak berarti jawaban gold sudah dilabeli manusia.

## 2. Urutan membaca implementasi sistem lengkap

Mulai dari admission/snapshot di workflow, lalu query preparation, retrieval,
fusion, hydration/reranking, context, generation dan validation. Untuk jalur data,
ikuti acquisition, worker dokumen, registry resolution, index writer dan publication.
Tabel berikut menunjukkan pemilik tiap tanggung jawab. Jalur tombol demo berbeda;
peta khususnya ada di [status aktual](07-implementation-status.md#peta-file-demo).

## 3. Go: alur produksi dan pemilik keputusan

```text
src/server/
  cmd/
    cli/                       collect, audit, submit, query-evidence, demo
    ingestion-worker/          Coordinator durable dokumen/proposal resolution
    semantic-gateway/          EXTRACT/RESOLVE model gateway
    api/                       Entrypoint HTTP produksi
  gen/regulagraph/v1/           Generated Protobuf; diubah lewat schema/codegen
  internal/
    domain/                    Identitas, invariant, admission dan kontrak internal
    workflows/                 Urutan tahap, dependency, retry, cancellation
    ingestion/sources/         Discovery/download/receipt/audit portal
    indexing/                  Admission batch, backend write, publication
    retrieval/
      query/                   Analyzer, normalizer, query artifacts, alias linking
      graph/                   Traversal dan path evidence
    answering/                 Context, generation, claim/citation validation
    adapters/
      postgres/                Registry, job, checkpoint, catalog, publication
      qdrant/                  Payload, collection, search, upsert/readback
      neo4j/                   Persistence graph dengan versioned support
      storage/                 Immutable artifacts, hash dan path confinement
      inference/               Native model client dan structured provider adapter
      worker/                  Client transport ke Rust worker
    api/                       HTTP translation; tidak memiliki algoritma retrieval
```

| Bagian | File yang dibuka | Tanggung jawab pada sistem lengkap |
| --- | --- | --- |
| Job → dokumen | [parse.go](../../src/server/internal/workflows/parse.go), [bind.go](../../src/server/internal/workflows/bind.go) | Dispatch, checkpoint dokumen dan binding registry |
| Proposal resolution | [semantic_resolution_executor.go](../../src/server/internal/workflows/semantic_resolution_executor.go), [semantic_resolution_model.go](../../src/server/internal/workflows/semantic_resolution_model.go) | Proposal model, validasi evidence, handoff keputusan dan review/resume |
| Otoritas identity | [registry_aliases.go](../../src/server/internal/adapters/postgres/registry_aliases.go), [registry_semantic.go](../../src/server/internal/adapters/postgres/registry_semantic.go) | Alias berscope, revision, canonical assignment dan keputusan registry |
| Index admission/write | [initial_prepare.go](../../src/server/internal/indexing/initial_prepare.go), [initial_writer.go](../../src/server/internal/indexing/initial_writer.go) | Validasi batch dan penulisan dense/sparse pada generation yang sesuai |
| Publication | [publication.go](../../src/server/internal/indexing/publication.go), [PG publication.go](../../src/server/internal/adapters/postgres/publication.go) | Reserve, staging, readiness dan perubahan pointer committed melalui CAS |
| Katalog indeks | [index_catalog.go](../../src/server/internal/adapters/postgres/index_catalog.go), [migration 0014](../../migrations/0014_index_catalog.up.sql) | Catalog binding dan authoritative record lookup |
| Satu snapshot per request | [rag_session.go](../../src/server/internal/workflows/rag_session.go) | Lease/pin sepanjang search, retry dan answer; scope temporal eksplisit |
| Factory backend query | [published_query.go](../../src/server/internal/workflows/published_query.go) | Katalog memilih collection/generation terpin |
| Parallel dense/lexical | [retrieval.go](../../src/server/internal/workflows/retrieval.go), [dense.go](../../src/server/internal/retrieval/dense.go), [lexical.go](../../src/server/internal/retrieval/lexical.go) | Penjadwalan branch menurut dependency, cancellation dan budget bersama |
| Query representation | [lexical.go](../../src/server/internal/retrieval/query/lexical.go), [sparse.go](../../src/server/internal/retrieval/query/sparse.go), [entity_linker.go](../../src/server/internal/retrieval/query/entity_linker.go) | Analyzer/sparse query dan alias linking berscope untuk entity seeds |
| Fusion/rerank | [fusion.go](../../src/server/internal/retrieval/fusion.go), [reranking.go](../../src/server/internal/retrieval/reranking.go) | RRF, deduplikasi evidence/version, pairing dan urutan skor cross-encoder |
| Source hydration | [hydration.go](../../src/server/internal/retrieval/hydration.go), [rag_hydration.go](../../src/server/internal/workflows/rag_hydration.go) | Byte/metadata otoritatif, temporal policy, dependency dan accounting |
| Context dan jawaban | [context_builder.go](../../src/server/internal/answering/context_builder.go), [generator.go](../../src/server/internal/answering/generator.go), [citations.go](../../src/server/internal/answering/citations.go), [validation.go](../../src/server/internal/answering/validation.go) | Packing bukti, generation, citation mapping dan validasi klaim |
| Qdrant transport | [collections.go](../../src/server/internal/adapters/qdrant/collections.go), [search.go](../../src/server/internal/adapters/qdrant/search.go) | Collection admission, search/filter, upsert dan readback |
| Neo4j/graph search | [store.go](../../src/server/internal/adapters/neo4j/store.go), [traversal.go](../../src/server/internal/retrieval/graph/traversal.go) | Mutation/traversal graph dengan filter snapshot, versi dan support |

Routing adaptif berada pada [classifier.go](../../src/server/internal/retrieval/query/classifier.go)
untuk kebutuhan query, serta [rag.go](../../src/server/internal/workflows/rag.go) dan
[answer.go](../../src/server/internal/workflows/answer.go) sebagai pemilik koordinasi
retrieval, pemeriksaan evidence gap dan revisi jawaban. Adapter database hanya
melakukan operasi backend; ia tidak menentukan kapan jawaban sudah memadai.
Keputusan retry mempertahankan snapshot dan budget request yang sama.

## 4. Rust: komputasi persiapan data

| Bagian | Lokasi | Tanggung jawab pada sistem lengkap |
| --- | --- | --- |
| Parsing | [pdf.rs](../../src/ingestion/src/document/parsing/pdf.rs), [ocr.rs](../../src/ingestion/src/document/parsing/ocr.rs) | PDF text layer, locator halaman, dan jalur OCR untuk scan |
| Normalisasi | [text.rs](../../src/ingestion/src/document/normalization/text.rs) | Teks canonical dan pemetaan ke offset sumber asli |
| Structural chunking | [structural.rs](../../src/ingestion/src/document/chunking/structural.rs), [builder.rs](../../src/ingestion/src/document/chunking/builder.rs), [tokenizer.rs](../../src/ingestion/src/document/chunking/tokenizer.rs) | Hierarchy dan chunk berbatas tokenizer dengan konteks induk |
| Versi dan perubahan | [provisions.rs](../../src/ingestion/src/document/versioning/provisions.rs), [change_detection.rs](../../src/ingestion/src/document/change_detection.rs) | Provision version, change events dan dependency perubahan |
| Extraction | [extractor.rs](../../src/ingestion/src/knowledge_graph/extraction/extractor.rs) | Hasil model menjadi assertion/proposal dengan evidence |
| Resolution | [blocking.rs](../../src/ingestion/src/knowledge_graph/resolution/blocking.rs), [resolver.rs](../../src/ingestion/src/knowledge_graph/resolution/resolver.rs) | Blocking kandidat dan proposal; PostgreSQL tetap otoritas keputusan |
| Assembly | [builder.rs](../../src/ingestion/src/knowledge_graph/assembly/builder.rs) | Materialisasi endpoint dan delta graph dari identity/support yang disahkan |
| Dense/sparse index | [build.rs](../../src/ingestion/src/indexing/build.rs), [inputs.rs](../../src/ingestion/src/indexing/inputs.rs), [dense.rs](../../src/ingestion/src/indexing/dense.rs), [lexical.rs](../../src/ingestion/src/indexing/lexical.rs), [statistics.rs](../../src/ingestion/src/indexing/statistics.rs) | Prepared input, vector dan frozen BM25; Go memiliki commit backend |
| Worker INDEX | [index.rs](../../src/ingestion/src/worker/index.rs) | Plan/manifest menjadi artefak batch beserta dependency manifest |

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

Native embedding/reranking berbeda dari generator jawaban. Generator memakai
adapter provider; embedding dan cross-encoder memakai layanan native ini.
Kontrak bundle/runtime dijelaskan pada [native-inference](../native-inference.md).

## 6. Kontrak, konfigurasi, evaluasi

| Lokasi | Penjelasan untuk interview |
| --- | --- |
| [src/contracts/proto](../../src/contracts/proto/README.md) | Satu sumber wire schema untuk worker/inference/evidence/jobs; binding generated tidak diedit manual |
| [schema-lock.json](../../src/contracts/schema-lock.json), [check_contracts.py](../../scripts/check_contracts.py) | Compatibility baseline dan pemeriksaan; bukan izin mengubah lock untuk meluluskan tes |
| [configs](../../configs/README.md) | Ontology, prompt, policy dan target; rahasia tetap di environment |
| [evaluation/runner.py](../../evaluation/runner.py) | Menilai run/gates dengan manifest dan eligibility yang sesuai |
| [evaluation/metrics](../../evaluation/metrics/README.md) | Retrieval, graph, answer, citation, runtime dipisah agar sumber masalah terlihat |
| [evaluation/datasets](../../evaluation/datasets/README.md) | Kontrak gold, annotation/review dan split yang mencegah leakage |
| [tests](../../tests/README.md) | Invariant dan regression tests; fixture PASS bukan model-quality PASS |

Saat menunjuk suatu test, jelaskan failure yang dicegah: citation palsu, snapshot
bercampur, skor reranker tertukar, artifact korup, atau retry menggandakan mutation.
“Jumlah test banyak” saja bukan penjelasan kualitas engineering.
