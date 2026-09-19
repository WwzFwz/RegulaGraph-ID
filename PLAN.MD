
**RegulaGraph-ID — Indonesian Regulatory Knowledge Intelligence** • A Hybrid GraphRAG system that helps users navigate fragmented and interconnected Indonesian regulations by answering factual, procedural, cross-document, and multi-hop questions with article-level citations and graph-based evidence. Parsed public regulatory documents into structure-aware chunks with parent context, extracted entities and relationships to automatically construct a Neo4j knowledge graph, and combined BM25, dense vector retrieval, graph traversal, and cross-encoder reranking before grounded LLM generation. Evaluated Vector RAG, Hybrid RAG, GraphRAG, and Hybrid GraphRAG on a hand-built Indonesian benchmark covering factual, multi-hop, typo-heavy, informal, and Indonesian-English code-switched queries. • Target stack: Go serving and orchestration; Rust document/graph processing; native C/C++ parsing and inference; Python offline evaluation and model tooling; BGE-M3, Qdrant, BM25, Neo4j, Cross-Encoder Reranker, RAGAS, PostgreSQL, and Docker.




| Bagian            | RegulaGraph-ID                                                                                                  |
| ----------------- | --------------------------------------------------------------------------------------------------------------- |
| **Masalah**       | Informasi regulasi tersebar, panjang, saling merujuk, berubah, dan sulit ditelusuri hanya dengan keyword search |
| **Input dokumen** | UU, PP, Perpres, Permen, peraturan lembaga, FAQ/pedoman resmi                                                   |
| **Input user**    | Pertanyaan Bahasa Indonesia                                                                                     |
| **Processing**    | parsing → chunking → entity/relation extraction → knowledge graph                                               |
| **Retrieval**     | BM25 + Vector Search + Graph Traversal                                                                          |
| **Reasoning**     | reranker + graph context + LLM                                                                                  |
| **Output**        | jawaban + regulasi + pasal + citation + hubungan antarregulasi                                                  |
| **Evaluation**    | Vector RAG vs Hybrid RAG vs GraphRAG vs Hybrid GraphRAG                                                         |




              Indonesian Regulations
                       │
                       ▼
              Document Processing
                       │
          ┌────────────┴────────────┐
          ▼                         ▼
    Semantic Chunking        Entity Extraction
                                    ↓
                            Relation Extraction
                                    ↓
                            Entity Resolution
                                    ↓
                               Neo4j KG

User Question
      │
      ▼
Query Normalization
      │
      ▼
Query Classification
      │
 ┌────┼───────────────┐
 │    │               │
 ▼    ▼               ▼
BM25 Vector         Graph
     Retrieval      Retrieval
 │    │               │
 └────┴───────┬───────┘
              ▼
       Retrieval Fusion
              │
              ▼
           Reranker
              │
              ▼
         Context Builder
              │
              ▼
             LLM
              │
              ▼
     Grounded Indonesian Answer
          + Citations
          + Related Articles
          + Graph Evidence

## Keputusan runtime dan struktur

Pembagian bahasa telah disetujui dengan prioritas latency dan throughput: Go di src/server, Rust di src/ingestion, C++ di src/inference, dan Python di evaluation/tooling. src/contracts menyimpan schema lintas runtime. Lihat [arsitektur](doc/architecture.md) dan [keputusan 0002](doc/decisions/0002-polyglot-runtime.md). Status proyek masih scaffold; deskripsi sistem di atas merupakan target, belum hasil implementasi atau benchmark.

## Desain lengkap sebelum implementasi

Pengguna memilih perancangan seluruh sistem sejak awal. [Desain menyeluruh](doc/system-design.md) menghubungkan use case, runtime, alur query/ingestion, graph, inference, deployment, dan evaluasi. [Kontrak sistem](doc/system-contracts.md) merancang seluruh record/API/RPC/event. [Storage dan konsistensi](doc/storage-consistency.md) menetapkan identity, temporal state, snapshot publication, incremental dependency, dan recovery.

Sumber corpus yang dipilih adalah Database Peraturan BPK, JDIH Kemkomdigi, serta JDIHN Nasional; [rencana corpus](doc/corpus-plan.md) menjelaskan akuisisi, provenance, deduplikasi, serta gold dataset. [Rencana implementasi](doc/development-plan.md) memecah pekerjaan menurut dependency beserta syarat selesai. Desain lengkap tidak berarti schema/layanan sudah aktif atau benchmark sudah tercapai; target required tetap berlaku tanpa perubahan.
