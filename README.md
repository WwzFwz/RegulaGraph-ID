# RegulaGraph-ID

Repositori ini menampung scaffold Hybrid GraphRAG untuk regulasi Indonesia dengan pemisahan runtime berdasarkan latency dan throughput. README ini menjadi peta fungsi, pemilik komponen, dan cara memeriksa struktur build.

**Status: scaffold lintas bahasa, belum ada pipeline atau layanan inference yang aktif.** Source produksi Python sebelumnya sudah digantikan; Python dipakai untuk evaluasi dan tooling model. Manifest build tersedia tanpa dependency database/model yang belum dipilih. Tidak ada hasil benchmark atau SLA yang diklaim.

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
│   ├── corpus-plan.md
│   ├── data-model.md
│   ├── development-plan.md
│   ├── Graph-Engineering-Athropic-Playbook.pdf
│   ├── README.md
│   ├── reference.md
│   ├── runtime-language-review.md
│   ├── storage-consistency.md
│   ├── system-contracts.md
│   └── system-design.md
├── evaluation/
│   ├── datasets/
│   │   ├── __init__.py
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
│   │   └── README.md
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
│   │   │   │   └── relations.rs
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
│   │   │   │   ├── chunks.go
│   │   │   │   ├── documents.go
│   │   │   │   ├── entities.go
│   │   │   │   ├── evidence.go
│   │   │   │   ├── README.md
│   │   │   │   └── relations.go
│   │   │   ├── indexing/
│   │   │   │   ├── publication.go
│   │   │   │   └── README.md
│   │   │   ├── ingestion/
│   │   │   │   ├── sources/
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
│   │   └── README.md
│   ├── integration/
│   │   └── README.md
│   ├── unit/
│   │   └── README.md
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

Pengelompokan kode produk dalam [src](src/README.md) dan konfigurasi runtime dalam [deployment](deployment/README.md) dicatat pada [keputusan 0004](doc/decisions/0004-product-source-layout.md). Tree di atas mencakup file yang dikelola; cache dan hasil build otomatis dikecualikan.

## Integrasi dan peran anak

Rancangan lengkap sebelum implementasi dimulai dari [system-design](doc/system-design.md), lalu [kontrak seluruh sistem](doc/system-contracts.md), [storage/snapshot/recovery](doc/storage-consistency.md), [rencana corpus](doc/corpus-plan.md), dan [tahapan pengembangan](doc/development-plan.md). Sumber yang dipilih adalah Database Peraturan BPK, JDIH Kemkomdigi, dan JDIHN. [Keputusan 0005](doc/decisions/0005-complete-system-design.md) mengikat cakupan desain penuh; schema Protobuf serta pipeline masih scaffold dan akan direalisasikan menurut dependency pekerjaan.

[Go server](src/server/README.md) memegang jalur request serta penjadwalan dan publikasi snapshot. [Rust worker](src/ingestion/README.md) menghasilkan batch dokumen/graph/indeks; commit storage dilakukan adapter Go. [Inference C++](src/inference/README.md) menampung wrapper runtime model, sedangkan engine parsing C/C++ dipanggil dari adapter Rust. [Contracts](src/contracts/README.md) menyatukan ID, versi, status, snapshot, dan offset teks.

Fusion, filter, context builder, serta citation tetap di proses Go yang sama. Parsing dan graph engineering dilakukan saat ingestion. [Evaluation](evaluation/README.md) mengakses endpoint atau artefak produksi melalui kontrak bersama, tanpa menyalin algoritma serving. [Tooling](tooling/README.md) menyiapkan model offline.

[Deployment](deployment/README.md) menentukan packaging dan wiring runtime. Compose berada di deployment/docker-compose.yml dan masih memiliki services kosong; belum tersedia Dockerfile atau layanan aktif. Workspace manifest tetap di root, sementara evaluation/tooling dijalankan offline.

## Akuisisi PDF dan metadata

Collector D01 sudah aktif untuk input batch URL dan discovery terbatas BPK/Kemkomdigi. Jalankan `go run ./src/server/cmd/cli collect -input configs/sources.txt` untuk mengunduh contoh PDF ke data/acquisition beserta metadata dan checksum. Run ulang memakai resume; `-refresh` memeriksa sumber kembali. Lihat [panduan akuisisi](doc/acquisition.md) untuk opsi dan batas dukungan JDIHN. Pipeline OCR/graph/retrieval serta kontrak produksi masih scaffold.

## Build scaffold

Dari root repositori, gunakan toolchain Go 1.26+ (toolchain workspace 1.26.8), Rust edition 2021, Python 3.11+, serta CMake 3.20+ dengan compiler C++17. Tidak diperlukan model atau SDK database untuk build scaffold.

```text
go build ./src/server/...
cargo check --workspace --offline
cmake -S src/inference -B .cache/inference-src
cmake --build .cache/inference-src --config Release
```

Build Go memvalidasi package dan entry point; entry point API tetap scaffold, sedangkan CLI collect sudah mengunduh PDF nyata dan metadata sumber. Rust saat ini berupa library dengan module tree, belum executable worker. C++ menghasilkan static library scaffold tanpa ONNX runtime atau model. Protobuf hanya mendeklarasikan syntax/package; codegen dan bentuk field belum tersedia. Packaging Python dalam pyproject.toml hanya mencakup evaluation dan tooling.

## Performa dan benchmark

Prioritas adalah p95/p99 latency, waktu sampai token jawaban pertama, throughput ingestion, dan efisiensi memori. Target numerik wajib sudah ditetapkan pada profil referensi asumsi dalam [benchmark-targets.yaml](configs/benchmark-targets.yaml); status REQUIRED_UNMEASURED. Hardware deployment aktual belum ditentukan. [Panduan target](doc/benchmark-targets.md) dan [kebijakan benchmark](doc/benchmark-policy.md) menjelaskan beban uji serta aturan negosiasi: agent tidak boleh menurunkan target yang gagal tanpa persetujuan pengguna.

Lihat [arsitektur](doc/architecture.md), [kontrak data](doc/data-model.md), dan [kajian bahasa](doc/runtime-language-review.md). Referensi pengguna di [doc/reference.md](doc/reference.md) dan PDF sumber dipertahankan.
