# RegulaGraph-ID

Repositori ini menampung pengembangan Hybrid GraphRAG untuk regulasi Indonesia dengan pemisahan runtime berdasarkan latency dan throughput. README ini menjadi peta fungsi, pemilik komponen, dan cara memeriksa struktur build.

**Demo lokal:** jalankan `powershell -ExecutionPolicy Bypass -File scripts/start_demo.ps1`
lalu buka http://127.0.0.1:8096. [Panduan demo](doc/interview-demo.md) menjelaskan
baseline BM25 + model lokal dari PDF nyata, contoh pertanyaan, dan batasnya.

**API evidence terpublikasi:** [panduan HTTP](doc/evidence-api.md) menyediakan
query vector/hybrid/graph/hybrid-graph pada snapshot yang sudah published, dengan
koneksi reusable. Graph-only tidak membutuhkan embedding service.
[Verifikasi](doc/verification-report-graph-api.md) mencakup request concurrent,
provenance dan cleanup lease. [CLI jawaban lokal](doc/local-answer.md) kini
tersambung dengan model/tokenizer terpin; `/v1/questions` menyediakan mode draft
HTTP opsional dengan model reusable, status UNREVIEWED/PARTIAL atau ABSTAIN.

**Publication graph:** [publish-graph](doc/graph-publication.md) mengaktifkan
inventory ASSEMBLE lengkap bersama indeks terverifikasi, dengan retry dan
perlindungan terhadap publication parsial. [prepare-graph](doc/graph-preparation.md)
menyiapkan seluruh sumber indeks yang sudah selesai RESOLVE dan menjadwalkan
ASSEMBLE secara atomik. [Reaffirmation](doc/graph-reaffirmation.md) mendukung sumber
lintas registry revision ketika seluruh konteks kandidat/dokumennya tetap valid.

**Status: graph/index publication, empat profil retrieval, native embedding/reranker,
serta jawaban draft lokal melalui CLI/API tersedia; acceptance release Hybrid
GraphRAG belum selesai.** Go/Rust/C++ memegang runtime produk; Python untuk
evaluasi/tooling offline. Tes integrasi menghubungkan Rust, PostgreSQL, Neo4j,
Qdrant dan model lokal pada fixture sumber sintetis. Ini belum membuktikan
kualitas extraction/resolution, ketepatan hukum, atau target latency pada corpus
regulasi nyata. Akuisisi/audit, kontrak, evaluator dan storage control-plane aktif;
coverage sumber, gold, pemilihan seluruh model, streaming, incremental lengkap
serta required benchmark masih perlu diselesaikan. Mulai kelanjutan dari
[rencana terkini](doc/development-plan.md), [runner evaluasi](doc/evaluation-runner.md),
dan [protokol verifikasi](doc/verification.md). Status lama di bagian bawah
mencatat tahap implementasi sebelumnya; checkpoint terkini berada pada rencana.

## Struktur dan cakupan

```text
RegulaGraph-ID/
├── .vscode/
│   └── settings.json
├── artifacts/
│   └── README.md
├── configs/
│   ├── benchmark-targets.yaml
│   ├── evaluation.yaml
│   ├── ingestion.yaml
│   ├── ontology-v1.jsonc
│   ├── listings.txt
│   ├── README.md
│   ├── retrieval.yaml
│   └── sources.txt
├── data/
│   └── README.md
├── deployment/
│   ├── docker-compose.yml
│   └── README.md
├── doc/
│   ├── decisions/
│   │   ├── 0001-scaffold-boundaries.md
│   │   ├── 0002-polyglot-runtime.md
│   │   ├── 0003-required-benchmark-targets.md
│   │   ├── 0004-product-source-layout.md
│   │   ├── 0005-complete-system-design.md
│   │   └── README.md
│   ├── acquisition.md
│   ├── architecture.md
│   ├── benchmark-policy.md
│   ├── benchmark-targets.md
│   ├── contracts-implementation.md
│   ├── corpus-plan.md
│   ├── data-model.md
│   ├── development-plan.md
│   ├── Graph-Engineering-Athropic-Playbook.pdf
│   ├── implementation-guide.md
│   ├── README.md
│   ├── reference.md
│   ├── runtime-language-review.md
│   ├── storage-consistency.md
│   ├── system-contracts.md
│   ├── system-design.md
│   ├── verification-contracts.md
│   ├── verification-pipeline.md
│   ├── verification-quality.md
│   ├── verification-report-c01.md
│   └── verification.md
├── evaluation/
│   ├── datasets/
│   │   ├── __init__.py
│   │   ├── evaluation.proto
│   │   ├── README.md
│   │   └── schema.py
│   ├── experiments/
│   │   ├── __init__.py
│   │   ├── profiles.yaml
│   │   └── README.md
│   ├── metrics/
│   │   ├── __init__.py
│   │   ├── answers.py
│   │   ├── citations.py
│   │   ├── graph.py
│   │   ├── README.md
│   │   ├── retrieval.py
│   │   └── runtime.py
│   ├── __init__.py
│   ├── README.md
│   └── runner.py
├── migrations/
│   └── README.md
├── scripts/
│   ├── check_contracts.py
│   ├── generate_contracts.py
│   └── README.md
├── src/
│   ├── contracts/
│   │   ├── proto/
│   │   │   ├── regulagraph/
│   │   │   │   ├── v1/
│   │   │   │   │   ├── answers.proto
│   │   │   │   │   ├── common.proto
│   │   │   │   │   ├── documents.proto
│   │   │   │   │   ├── evidence.proto
│   │   │   │   │   ├── graph.proto
│   │   │   │   │   ├── inference.proto
│   │   │   │   │   ├── jobs.proto
│   │   │   │   │   └── README.md
│   │   │   │   └── README.md
│   │   │   └── README.md
│   │   ├── CMakeLists.txt
│   │   ├── README.md
│   │   ├── schema-lock.json
│   │   ├── wire_validation.cpp
│   │   └── wire_validation.hpp
│   ├── inference/
│   │   ├── include/
│   │   │   ├── regulagraph/
│   │   │   │   ├── inference/
│   │   │   │   │   ├── batching.hpp
│   │   │   │   │   ├── cross_encoder.hpp
│   │   │   │   │   ├── embeddings.hpp
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── runtime.hpp
│   │   │   │   └── README.md
│   │   │   └── README.md
│   │   ├── src/
│   │   │   ├── batching.cpp
│   │   │   ├── cross_encoder.cpp
│   │   │   ├── embeddings.cpp
│   │   │   ├── README.md
│   │   │   └── runtime.cpp
│   │   ├── CMakeLists.txt
│   │   └── README.md
│   ├── ingestion/
│   │   ├── src/
│   │   │   ├── adapters/
│   │   │   │   ├── inference.rs
│   │   │   │   ├── mod.rs
│   │   │   │   ├── pdf_engine.rs
│   │   │   │   ├── README.md
│   │   │   │   └── storage.rs
│   │   │   ├── document/
│   │   │   │   ├── chunking/
│   │   │   │   │   ├── builder.rs
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   ├── parents.rs
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── structural.rs
│   │   │   │   ├── normalization/
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── text.rs
│   │   │   │   ├── parsing/
│   │   │   │   │   ├── html.rs
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   ├── ocr.rs
│   │   │   │   │   ├── pdf.rs
│   │   │   │   │   └── README.md
│   │   │   │   ├── versioning/
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   ├── provisions.rs
│   │   │   │   │   └── README.md
│   │   │   │   ├── change_detection.rs
│   │   │   │   ├── mod.rs
│   │   │   │   └── README.md
│   │   │   ├── domain/
│   │   │   │   ├── chunks.rs
│   │   │   │   ├── documents.rs
│   │   │   │   ├── entities.rs
│   │   │   │   ├── evidence.rs
│   │   │   │   ├── mod.rs
│   │   │   │   ├── README.md
│   │   │   │   ├── relations.rs
│   │   │   │   └── wire.rs
│   │   │   ├── indexing/
│   │   │   │   ├── dense.rs
│   │   │   │   ├── lexical.rs
│   │   │   │   ├── mod.rs
│   │   │   │   └── README.md
│   │   │   ├── knowledge_graph/
│   │   │   │   ├── assembly/
│   │   │   │   │   ├── builder.rs
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   └── README.md
│   │   │   │   ├── extraction/
│   │   │   │   │   ├── extractor.rs
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   ├── prompts.rs
│   │   │   │   │   └── README.md
│   │   │   │   ├── resolution/
│   │   │   │   │   ├── aliases.rs
│   │   │   │   │   ├── blocking.rs
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── resolver.rs
│   │   │   │   ├── summarization/
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   ├── profiles.rs
│   │   │   │   │   └── README.md
│   │   │   │   ├── validation/
│   │   │   │   │   ├── checks.rs
│   │   │   │   │   ├── mod.rs
│   │   │   │   │   └── README.md
│   │   │   │   ├── mod.rs
│   │   │   │   ├── README.md
│   │   │   │   └── schema.rs
│   │   │   ├── lib.rs
│   │   │   └── README.md
│   │   ├── build.rs
│   │   ├── Cargo.toml
│   │   └── README.md
│   ├── server/
│   │   ├── cmd/
│   │   │   ├── api/
│   │   │   │   ├── main.go
│   │   │   │   └── README.md
│   │   │   ├── cli/
│   │   │   │   ├── main.go
│   │   │   │   ├── main_test.go
│   │   │   │   └── README.md
│   │   │   └── README.md
│   │   ├── gen/
│   │   │   └── regulagraph/
│   │   │       └── v1/
│   │   │           ├── answers.pb.go
│   │   │           ├── common.pb.go
│   │   │           ├── documents.pb.go
│   │   │           ├── evaluation.pb.go
│   │   │           ├── evidence.pb.go
│   │   │           ├── graph.pb.go
│   │   │           ├── inference.pb.go
│   │   │           └── jobs.pb.go
│   │   ├── internal/
│   │   │   ├── adapters/
│   │   │   │   ├── inference/
│   │   │   │   │   ├── cross_encoder.go
│   │   │   │   │   ├── embeddings.go
│   │   │   │   │   ├── llm.go
│   │   │   │   │   └── README.md
│   │   │   │   ├── neo4j/
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── store.go
│   │   │   │   ├── postgres/
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── repository.go
│   │   │   │   ├── qdrant/
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── store.go
│   │   │   │   ├── storage/
│   │   │   │   │   ├── files.go
│   │   │   │   │   └── README.md
│   │   │   │   ├── worker/
│   │   │   │   │   ├── client.go
│   │   │   │   │   └── README.md
│   │   │   │   └── README.md
│   │   │   ├── answering/
│   │   │   │   ├── citations.go
│   │   │   │   ├── context_builder.go
│   │   │   │   ├── generator.go
│   │   │   │   ├── README.md
│   │   │   │   └── validation.go
│   │   │   ├── api/
│   │   │   │   ├── routes/
│   │   │   │   │   ├── documents.go
│   │   │   │   │   ├── health.go
│   │   │   │   │   ├── questions.go
│   │   │   │   │   └── README.md
│   │   │   │   ├── schemas/
│   │   │   │   │   ├── documents.go
│   │   │   │   │   ├── errors.go
│   │   │   │   │   ├── questions.go
│   │   │   │   │   └── README.md
│   │   │   │   ├── dependencies.go
│   │   │   │   ├── README.md
│   │   │   │   └── server.go
│   │   │   ├── config/
│   │   │   │   ├── config.go
│   │   │   │   └── README.md
│   │   │   ├── domain/
│   │   │   │   ├── answers.go
│   │   │   │   ├── boundaries.go
│   │   │   │   ├── boundaries_test.go
│   │   │   │   ├── chunks.go
│   │   │   │   ├── documents.go
│   │   │   │   ├── entities.go
│   │   │   │   ├── evidence.go
│   │   │   │   ├── README.md
│   │   │   │   ├── relations.go
│   │   │   │   ├── wire.go
│   │   │   │   └── wire_test.go
│   │   │   ├── indexing/
│   │   │   │   ├── publication.go
│   │   │   │   └── README.md
│   │   │   ├── ingestion/
│   │   │   │   ├── sources/
│   │   │   │   │   ├── budget.go
│   │   │   │   │   ├── budget_test.go
│   │   │   │   │   ├── collector_test.go
│   │   │   │   │   ├── download.go
│   │   │   │   │   ├── html.go
│   │   │   │   │   ├── http.go
│   │   │   │   │   ├── inventory.go
│   │   │   │   │   ├── listing.go
│   │   │   │   │   ├── local.go
│   │   │   │   │   ├── official.go
│   │   │   │   │   └── README.md
│   │   │   │   └── README.md
│   │   │   ├── retrieval/
│   │   │   │   ├── graph/
│   │   │   │   │   ├── evidence.go
│   │   │   │   │   ├── README.md
│   │   │   │   │   └── traversal.go
│   │   │   │   ├── query/
│   │   │   │   │   ├── classifier.go
│   │   │   │   │   ├── entity_linker.go
│   │   │   │   │   ├── normalizer.go
│   │   │   │   │   └── README.md
│   │   │   │   ├── dense.go
│   │   │   │   ├── filters.go
│   │   │   │   ├── fusion.go
│   │   │   │   ├── lexical.go
│   │   │   │   ├── README.md
│   │   │   │   └── reranking.go
│   │   │   ├── workflows/
│   │   │   │   ├── answer.go
│   │   │   │   ├── collect.go
│   │   │   │   ├── collect_test.go
│   │   │   │   ├── discover.go
│   │   │   │   ├── discover_test.go
│   │   │   │   ├── ingest.go
│   │   │   │   ├── README.md
│   │   │   │   └── update.go
│   │   │   └── README.md
│   │   ├── go.mod
│   │   ├── go.sum
│   │   └── README.md
│   └── README.md
├── tests/
│   ├── end_to_end/
│   │   └── README.md
│   ├── fixtures/
│   │   ├── README.md
│   │   └── wire-cases.json
│   ├── integration/
│   │   ├── README.md
│   │   ├── wire_cpp.cpp
│   │   └── wire_roundtrip.py
│   ├── unit/
│   │   ├── README.md
│   │   └── test_evaluation_contracts.py
│   └── README.md
├── tooling/
│   ├── models/
│   │   ├── __init__.py
│   │   ├── export.py
│   │   ├── parity.py
│   │   └── README.md
│   ├── __init__.py
│   └── README.md
├── .env.example
├── .gitignore
├── AGENTS.md
├── Cargo.lock
├── Cargo.toml
├── go.work
├── PLAN.MD
├── pyproject.toml
└── README.md
```

Baca README setiap folder sebelum menambah fungsi. [AGENTS.md](AGENTS.md) menetapkan bahwa komponen di luar cakupan folder perlu dibicarakan terlebih dahulu. Migrasi Go/Rust/C++ ini telah disetujui pengguna dan dicatat pada [keputusan 0002](doc/decisions/0002-polyglot-runtime.md).

Pengelompokan kode produk dalam [src](src/README.md) dan konfigurasi runtime dalam [deployment](deployment/README.md) dicatat pada [keputusan 0004](doc/decisions/0004-product-source-layout.md). Tree di atas mencakup file yang dilacak/dikelola, termasuk binding Go yang dihasilkan; cache, corpus lokal dan hasil build lain dikecualikan.

## Integrasi dan peran anak

Rancangan lengkap sebelum implementasi dimulai dari [system-design](doc/system-design.md), lalu [kontrak seluruh sistem](doc/system-contracts.md), [storage/snapshot/recovery](doc/storage-consistency.md), [rencana corpus](doc/corpus-plan.md), dan [tahapan pengembangan](doc/development-plan.md). Sumber yang dipilih adalah Database Peraturan BPK, JDIH Kemkomdigi, dan JDIHN. [Keputusan 0005](doc/decisions/0005-complete-system-design.md) mengikat cakupan desain penuh; schema Protobuf C01 tersedia; implementasi pipeline mengikuti dependency pekerjaan.

[Go server](src/server/README.md) memegang jalur request serta penjadwalan dan publikasi snapshot. [Rust worker](src/ingestion/README.md) menghasilkan batch dokumen/graph/indeks; commit storage dilakukan adapter Go. [Inference C++](src/inference/README.md) menampung wrapper runtime model, sedangkan engine parsing C/C++ dipanggil dari adapter Rust. [Contracts](src/contracts/README.md) menyatukan ID, versi, status, snapshot, dan offset teks.

Fusion, filter, context builder, serta citation tetap di proses Go yang sama. Parsing dan graph engineering dilakukan saat ingestion. [Evaluation](evaluation/README.md) mengakses endpoint atau artefak produksi melalui kontrak bersama, tanpa menyalin algoritma serving. [Tooling](tooling/README.md) menyiapkan model offline.

[Deployment](deployment/README.md) menentukan packaging dan wiring runtime. Compose berada di deployment/docker-compose.yml dan masih memiliki services kosong; belum tersedia Dockerfile atau layanan aktif. Workspace manifest tetap di root, sementara evaluation/tooling dijalankan offline.

## Akuisisi PDF dan metadata

Collector D01 sudah aktif untuk input batch URL dan discovery terbatas BPK/Kemkomdigi. Jalankan `go run ./src/server/cmd/cli collect -input configs/sources.txt` untuk mengunduh sumber, lalu `go run ./src/server/cmd/cli audit -out data/acquisition` untuk memverifikasi queue, record, observation history, HTML, receipt, dan seluruh hash PDF. Run ulang collector memakai resume; `-refresh` memeriksa sumber kembali. Lihat [panduan akuisisi](doc/acquisition.md) untuk opsi dan batas dukungan JDIHN. Pipeline OCR/graph/retrieval belum aktif. Inventory lokal saat ini mengikat 616 record, 619 observation, dan 650 PDF unik (2.999.240.002 bytes); 21 record incomplete tetap terlihat sebagai warning dan 2.545 URL queue belum diambil.

## Build dan verifikasi

Dari root repositori, gunakan toolchain Go 1.26+ (toolchain workspace 1.26.8), Rust edition 2021, Python 3.11+, serta CMake 3.20+ dengan compiler C++17. Tidak diperlukan model atau SDK database untuk build scaffold.

```text
go build ./src/server/...
cargo check --workspace --offline
cmake -S src/inference -B .cache/inference-src
cmake --build .cache/inference-src --config Release
```

Build Go memvalidasi package, collector, storage/control-plane, client Worker, coordinator PARSE sampai EXTRACT, dan Semantic.ExtractBatch Gateway; entry point API query tetap scaffold. Rust menyediakan worker Tonic untuk PARSE, STRUCTURE, CHUNK, EXTRACT, dan INDEX terkonfigurasi. Go memverifikasi bytes/dependency/checkpoint sampai EXTRACT, termasuk model/prompt pin dan exact source evidence. C++ menyediakan layanan ONNX native terpin; acceptance kualitas model dan benchmark tetap terbuka. C01 menyediakan schema lintas bahasa, empat service descriptor, codegen dan validator. Worker INDEX merakit artefak plan-bound; dispatch durable, katalog/writer/publication penuh dan antarmuka RAG end-to-end belum tersambung. Lihat [kontrak INDEX](doc/index-build.md). Packaging Python hanya mencakup evaluation dan tooling.

## Performa dan benchmark

Prioritas adalah p95/p99 latency, waktu sampai token jawaban pertama, throughput ingestion, dan efisiensi memori. Target numerik wajib sudah ditetapkan pada profil referensi asumsi dalam [benchmark-targets.yaml](configs/benchmark-targets.yaml); status REQUIRED_UNMEASURED. Hardware deployment aktual belum ditentukan. [Panduan target](doc/benchmark-targets.md) dan [kebijakan benchmark](doc/benchmark-policy.md) menjelaskan beban uji serta aturan negosiasi: agent tidak boleh menurunkan target yang gagal tanpa persetujuan pengguna.

Lihat [arsitektur](doc/architecture.md), [kontrak data](doc/data-model.md), dan [kajian bahasa](doc/runtime-language-review.md). Referensi pengguna di [doc/reference.md](doc/reference.md) dan PDF sumber dipertahankan.

Gateway RESOLVE kontekstual dan workflow proposal audit/replay tersedia sebagai komponen callable, termasuk katalog EXTRACT atomik serta bukti kandidat lintas dokumen dengan citation dua sisi. Smoke structured output Ollama lokal telah berhasil pada fixture sintetis; dispatch otomatis, model produksi, serta acceptance kualitas/performa belum selesai. Lihat [integrasi resolusi](doc/semantic-resolution.md).

Native embedding BGE-M3 dan reranker BGE v2 M3 kini tersedia melalui layanan C++ ONNX dan client batch Go/Rust. Status/batas bukti serta build lokal berada di [native inference](doc/native-inference.md); acceptance kualitas/performa tetap belum lulus.
