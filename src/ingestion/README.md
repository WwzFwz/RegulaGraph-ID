# src/ingestion

Worker INDEX kini menghasilkan IndexBatch dari immutable plan, text artifact,
dictionary/statistik terpin dan native embedding. Go memvalidasi proyeksi output
terhadap sumber. Ini menutup paket worker, sementara dispatch durable coordinator,
katalog/writer dan publication masih terbuka. Lihat [kontrak INDEX](../../doc/index-build.md).

Crate Rust untuk pemrosesan dokumen serta transformasi knowledge graph dan indeks secara batch. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../AGENTS.md).

## Peran dan integrasi anak

Rust menyiapkan data dan artefak immutable, Go mengoordinasikan job serta commit/publikasi, dan C/C++ menyediakan engine parsing/inference. Transport gRPC batch tahap PARSE, STRUCTURE, dan CHUNK menerima dispatch dari coordinator Go melalui job durable dan checkpoint fenced.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Library `knowledge_graph/assembly/delta.rs` menghasilkan GraphDelta upsert berbukti
dari EXTRACT/RESOLVE, registry rows dan bytes teks; dependency/visibility dipertahankan.
Worker ASSEMBLE tersedia melalui plan terpin; coordinator graph, incremental closure dan Neo4j belum tersambung; [kontrak](../../doc/graph-delta.md).

Assembly graph memiliki library canonical assertion/support setelah endpoint
resolution, dengan provenance terpisah dan mapping ID. Ini belum GraphDelta/Neo4j;
lihat [kontrak identitas](../../doc/graph-canonical-identity.md).

Subfolder: [src/](src/README.md), termasuk [worker](src/worker/README.md) dan executable [bin](src/bin/README.md).

Berkas: [Cargo.toml](Cargo.toml).

## Benchmark dan perhatian performa

Ukur halaman/detik, mention/edge per detik, p95 waktu job, scaling core, peak RSS, dan freshness lag. Nilai sasaran wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml).

Ikuti [kebijakan benchmark](../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Worker EXTRACT memuat ontology berversi dengan hash terpin, memeriksa pin pada request/manifest Semantic Gateway, dan menolak graph typed yang tidak sesuai sebelum persistence. Gate ini belum mengukur akurasi extraction.

Boundary parser PDFium, normalizer, structural chunking, proyeksi/persistence C01, incremental planner, selector timeline, blocking kandidat, dan helper alias LINK sourced aktif sebagai library. Executable worker Tonic menjalankan PARSE dari PDF terverifikasi, STRUCTURE dari `DocumentBatch` immutable, lalu CHUNK dari batch BIND lengkap. CHUNK memverifikasi ulang raw/normalized/mapping dan hierarchy, memasangkan node ke `ProvisionVersion` registry-owned secara eksak, memakai tokenizer Hugging Face hash-pinned, serta menghasilkan chunk parent-aware dengan batas token. Stage RESOLVE, receipt registry terintegrasi, extraction change-event, tabel, coordinator/publication graph, OCR, gold temporal, object storage, full-rebuild equivalence, dan acceptance produksi belum aktif.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [build.rs](build.rs). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../doc/contracts-implementation.md).

Adapter `NativeEmbeddingClient` menyediakan batch embedding async melalui C++ C01 untuk integrasi indexing. Client ini sudah diuji pada transport nyata; publikasi dense index tetap pekerjaan X01.

Builder analyzer/statistik BM25 typed kini tersedia dengan reader Go dari bytes
yang sama. Ini menutup handoff representasi lexical, tetapi bukan dispatch coordinator INDEX
atau publication; lihat [kontrak lexical](../../doc/lexical-generation.md).

Worker offline regulagraph-lexical menghitung vocabulary/statistik dari chunk
terverifikasi menggunakan rendering INDEX yang sama. Handoff allocator Go telah
diuji lewat executable; [panduan](../../doc/lexical-population.md) menyatakan
prasyarat serta registration/scheduling yang masih perlu disambungkan.
