# Rencana implementasi X01: indeks berversi dan pencarian nyata

Dokumen ini memecah pekerjaan X01 menjadi komponen, fungsi yang diusulkan, serta pemeriksaan
input/proses/output. Disusun dari revision `17bba83` atas permintaan pengguna untuk merencanakan
kode sebelum melanjutkan implementasi. Nama file/fungsi baru di bawah adalah usulan, bukan fitur
yang sudah tersedia. Dokumen ini mengikuti [kontrak](system-contracts.md),
[konsistensi storage](storage-consistency.md), dan [verifikasi](verification.md).

## Tujuan dan urutan pekerjaan

Hasil X01 adalah alur chunk terverifikasi → representasi dense/BM25 → IndexBatch immutable →
penulisan Qdrant oleh Go → verifikasi read → publication snapshot → pencarian pada snapshot
yang dipin. Output pencarian mempertahankan chunk, sumber, versi pasal, identitas model, dan
ranking asal agar Q01 dapat menggabungkan kandidat dan A01 dapat menghidrasi bukti.

Sesuai arahan terbaru, anotasi gold lengkap G01 dijadwalkan setelah alur implementasi
X01 → K01 → Q01 → A01 → U01 tersambung. Schema evaluasi, provenance, raw observations,
fixture perilaku, dan pengujian dokumen nyata tetap disiapkan saat coding. Setelah G01,
gunakan dev untuk perbaikan/pemilihan model dan test terpisah untuk acceptance B01.
Penundaan gold tidak mengubah target, minimum sampel, atau syarat kelulusan.

Fondasi yang dipakai ulang: `NativeEmbeddingClient::embed_batch`, `Bm25Statistics`,
artifact `ReadVerified`/content-addressed storage, job/checkpoint/fence S01,
`PublicationCoordinator.Reserve/Stage/Acknowledge/Publish/Abort`, serta snapshot read pin.
Runtime C++ tidak diberi tanggung jawab indexing, dan Rust tidak menulis Qdrant/PostgreSQL.

Kemajuan kontrak saat ini: `IndexFilterFormat.PAIRED_PROVISION_V1` pada
`IndexGeneration` dan `IndexProvisionFilter` pada `FilterMetadata` sudah dibuat
secara aditif. Gate Go memeriksa pasangan filter dan memproyeksikan fakta
regulasi/versi/interval/status/blob dari satu `DocumentBatch` tervalidasi.
Generator, writer, pembaca backend, dan publication masih belum aktif. Generation
paired **tidak boleh diterbitkan** sebelum setiap pembaca yang bisa menerima
route query memahami format tersebut; pembaca binary lama mengabaikan field
baru dan dapat kehilangan filter hukum walau decode berhasil. Binding fisik
generation serta rollout pembaca harus menolak format tidak dikenal sebelum
melayani traffic. Array legacy hanya boleh dibaca pada generation lama dengan
semantik yang dibuktikan; writer paired baru menolaknya agar tidak menggabungkan
interval dan status dari versi yang berbeda.

## 1. Audit kontrak dan identitas representasi

`IndexGeneration`, `IndexRecord`, `IndexBatch`, `BackendReceipt`, dan worker output
`index_batch` sudah ada di C01. Bentuk batch publik tersebut digunakan kembali.
Sebelum implementasi writer, tutup kebutuhan yang masih hanya berupa ArtifactRef atau
belum mempunyai hubungan field yang cukup tegas:

- Pesan artefak bertipe untuk analyzer, dictionary, statistics, dan rencana build. Usulan
  nama: `LexicalAnalyzerManifest`, `LexicalDictionary`, `LexicalStatistics`, `IndexBuildPlan`.
  Statistics menyatakan populasi chunk/snapshot, N/df/total length, formula, k1/b,
  serta cara memperlakukan dokumen dengan token nol; dictionary merekam lineage/revision.
- Rencana build mengikat daftar artefak input, membership corpus, base/target snapshot,
  generation, producer, kebijakan rendering teks embedding, upsert/closure, dan budget.
  Peran artefak pada request INDEX harus eksplisit; jangan menebak dari urutan `sources`.
- Hubungan versi pasal dengan interval/status legal harus dipertahankan. Field array terpisah
  pada `FilterMetadata` tidak boleh diasumsikan bisa di-zip tanpa kontrak. Audit kebutuhan
  record tambahan `IndexProvisionFilter` yang membawa version ID, regulation, interval,
  status, dan jurisdiksi sebagai satu objek; data berasal dari versi pasal terverifikasi.
- Pemetaan logical generation ke konfigurasi fisik indeks, kebijakan compatibility/reuse,
  dan profil/capability snapshot perlu mempunyai sumber state tepercaya. Query tidak
  menerima nama collection atau klaim kompatibilitas bebas dari pengguna.
  Pin policy rendering input embedding serta distance/normalization dalam identitas
  representation; perubahan konteks/prefix tidak boleh lolos sebagai konfigurasi yang sama.
  Usulan artefak `IndexReadinessProof` mengikat generation, target sequence, expected data
  digest, replica/read routing dan consistency policy yang dibuktikan sebelum receipt ready.

Bila perlu field/pesan tambahan, buat perubahan additive pada proto pemiliknya beserta
validator dan binding Go/Rust/C++/Python. Tentukan tag sesudah audit descriptor. Pertahankan
baseline compatibility; jangan mengganti schema-lock untuk menutupi perubahan breaking.
Validasi baru bersifat stage-specific agar tidak tiba-tiba menolak artefak lama yang sah.

Usulan fungsi murni: `ValidateIndexBuildPlan`, `ValidateIndexBatchClosure`,
`ValidateIndexReadCompatibility`, `FingerprintIndexGeneration`, `FingerprintIndexRecord`,
`ComputeIndexOperationsChecksum`.
Pemiliknya `src/server/internal/domain/index_*.go` dan `src/ingestion/src/domain/index_*.rs`.
Fingerprint menggunakan encoding kanonik versioned yang diuji lintas bahasa, bukan byte
serialisasi protobuf/JSON arbitrer. Checksum operasi membedakan upsert dan closure serta
mengikat payload dan expected prior state; urutan set dibekukan, urutan semantik dipertahankan.

## 2. Subkomponen dan fungsi yang diusulkan

Nama file di kolom lokasi menunjukkan pemisahan fungsi. File ditambahkan ketika ada
implementasi koheren, bukan membuat semua scaffold lebih dahulu. Seluruh lokasi menggunakan
folder dengan cakupan yang telah tersedia; README anak dibaca lagi sebelum penulisan kode.

| Komponen / lokasi | Fungsi yang direncanakan | Input → output dan perhatian utama |
| --- | --- | --- |
| Input indeks: `src/ingestion/src/indexing/inputs.rs` | `load_index_inputs`, `render_embedding_input` | DocumentBatch dan artefak teks terverifikasi → chunk input, source/version refs, hash teks aktual. Pertahankan batas UTF-8 dan bedakan konteks tambahan dari teks bukti primer. |
| Analyzer: `src/ingestion/src/indexing/analyzer.rs`; `src/server/internal/retrieval/query/lexical.go` | `analyze_document`, `AnalyzeLexicalQuery` | Aturan analyzer terpin dan teks → terms/frequencies. Go dan Rust memakai spesifikasi serta fixture yang sama. |
| Dictionary: `src/ingestion/src/indexing/dictionary.rs`; `src/server/internal/adapters/postgres/index_dictionary.go` | `collect_terms`, `AllocateLexicalTerms`, `FreezeDictionary` | Terms satu batch → ID term append-only dan artefak dictionary immutable. Go mengalokasikan ID secara transaksi; Rust memakai hasil terpin. |
| BM25: `src/ingestion/src/indexing/lexical.rs`, `statistics.rs`; reader Go `retrieval/query/lexical.go` | `freeze_statistics`, `encode_document_weights`, `EncodeLexicalQuery` | Statistik corpus/analyzer/dictionary/formula → sparse document/query vector dengan score yang setara fungsi BM25 referensi. |
| Dense: `src/ingestion/src/indexing/dense.rs` | `build_dense_records`, `embedding_reuse_key` | Input chunk dan model terpin → vector melalui native client yang sudah ada. Reuse berdasarkan byte input/model/policy yang sama, bukan hanya nama chunk. |
| Perakitan batch: `src/ingestion/src/indexing/batch.rs` | `build_index_batch`, `validate_index_batch` | Dense, sparse, dependencies, metadata versi → IndexBatch dan checksum immutable. Hasil parsial tidak menjadi batch siap publish. |
| Katalog indeks: `src/server/internal/adapters/postgres/index_generations.go`, `index_points.go` | `RegisterIndexGeneration`, `ResolveIndexGeneration`, `ReservePointIDs` | Manifest dan full record IDs → binding fisik/generation tervalidasi serta point IDs collision-checked. Constraint/migrasi mengikuti folder migrations yang ada. |
| Qdrant: `src/server/internal/adapters/qdrant/store.go`, `collections.go`, `points.go`, `filters.go`, `search.go`, `readiness.go` | `EnsureGeneration`, `UpsertBatch`, `ApplyClosures`, `BuildSnapshotFilter`, `SearchDense`, `SearchSparse`, `VerifyGenerationReady` | Operasi terikat generation/snapshot → write/read nyata dan bukti readiness. SDK connection reusable; budget waktu/bytes/item eksplisit. |
| Commit/recovery: `src/server/internal/indexing/writer.go`, `recovery.go`, `publication.go` | `StageIndexBatch`, `DispatchIndexOperations`, `ReconcileIndexPublication`, `CompensateIndexOperations` | Batch + reservation/fence → operation ledger, before-images, receipts, lalu coordinator publication yang sudah ada. |
| Worker/coordinator: `src/ingestion/src/worker/index.rs`; `src/server/internal/workflows/index.go` | `execute_index`, `IndexExecutor.RunOnce`, `prepareIndexInputs`, `commitIndexCheckpoint` | Claim job/plan → worker output artefak → pemeriksaan ulang dan checkpoint Go. Poll cancellation dan hubungkan `RenewLease` untuk pekerjaan panjang. |
| Wiring dan inspeksi: `src/server/cmd/ingestion-worker`, `src/server/cmd/cli/index.go` | `runBuildIndex`, `runInspectIndex`, `runSearchIndex` | Konfigurasi/argumen tervalidasi → workflow bersama dan keluaran audit. CLI tidak menggandakan builder/search/publisher. |

Komponen config (`configs` dan loader Go/Rust pemiliknya) mem-pin endpoint, manifest,
analyzer/formula, policy refresh, batas concurrency/payload, serta identitas server/SDK.
Konfigurasi harus benar-benar dikonsumsi runtime dan memengaruhi fingerprint; jangan menambah
YAML yang hanya tampak aktif. Nomor migrasi dipilih dari keadaan repo saat implementasi.

## 3. Keputusan algoritma dan performa

### Analyzer dan BM25

Rancangan awal analyzer mempertahankan teks asli dan memakai normalisasi NFC serta aturan
case/Unicode yang dipin untuk lookup. Token angka, tahun, nomor regulasi, pasal/ayat, dan
negasi mendapat fixture eksplisit. Tidak ada stemming/stopword removal otomatis pada baseline;
perubahan berikutnya memerlukan generation baru dan pengukuran kualitas.

Vocabulary memakai ID uint32 append-only yang dialokasikan Go secara batch. Overflow atau
collision merupakan error eksplisit; hashing term ke ruang kecil tanpa pemeriksaan tidak
dipakai. Query membaca dictionary/statistics terpin, tanpa RPC per token atau mutasi registry
pada jalur query. Term yang belum terdaftar tidak diberi ID palsu; jumlah OOV dicatat.

Formula awal mempertahankan perilaku `Bm25Statistics.score`: bobot dokumen memuat komponen
TF/normalisasi panjang; query memuat query-frequency dan IDF dari statistics generation yang
sama. Nilai numerik k1/b menjadi konfigurasi eksplisit, bukan konstanta tersembunyi.
Dot product sparse dibandingkan dengan referensi pada corpus beku, termasuk query berulang,
term langka/baru, dokumen kosong, dan corpus kosong. Generation corpus kosong tidak ready.

Sesuai desain storage, update biasa memakai statistik frozen; refresh statistik adalah
operasi eksplisit yang membentuk generation baru dan me-reweight semua record terdampak.
Dictionary yang bertambah tidak menomori ulang term lama. Term baru yang sudah terdaftar
tetapi tidak ada di statistik frozen memakai df=0 sesuai formula/smoothing versioned.
Drift/OOV/usia statistik dicatat; kualitasnya tetap harus dinilai setelah gold tersedia.

Dynamic IDF Qdrant dinonaktifkan untuk jalur ini agar statistik mengikuti manifest kita.
Dokumentasi Qdrant menjelaskan bahwa filter payload biasa tidak otomatis mengisolasi IDF;
fitur pengaturan IDF corpus yang tersedia pada versi tertentu juga harus diuji sebelum
menjadi alternatif desain. [Sumber Qdrant](https://qdrant.tech/documentation/manage-data/multitenancy/)

### Generation fisik dan reuse

Rancangan yang perlu dibuktikan pada paket kontrak: logical `IndexGeneration` mereferensikan
model, input-rendering/distance policy, analyzer, dictionary revision, dan statistics. Backend binding membedakan perubahan
kompatibel append-only dictionary dari perubahan representation yang mengubah bobot/ruang
vector. Compatibility harus dibuktikan dari manifest/lineage, bukan kemiripan nama versi.

Rekomendasi fisik adalah named dense/sparse vectors dalam collection per keluarga representasi
kompatibel. Penambahan dictionary dengan ID lama tetap sama dapat memakai collection yang sama
melalui binding generation tepercaya; payload menyimpan origin generation dan physical family.
Query snapshot hanya menerima record visible dengan origin generation yang terbukti kompatibel.
Reader/validator tidak boleh sembarang menolak semua record lama atau menerima semua generation.
Fixture wajib menunjukkan record unchanged tetap ditemukan setelah dictionary bertambah,
sementara pergantian model/analyzer/statistics tidak tercampur.

Kompatibilitas dictionary berarah: pembaca yang mem-pin D2 boleh memakai record D1 jika D1
merupakan ancestor yang terbukti, normalisasi/ID lama identik, dan semua term ID record
tercakup D2. Physical family yang sama tidak otomatis membolehkan pembaca D1 memakai record
dari revision masa depan atau sibling. Uji ancestor reuse, future/sibling rejection,
ID reassignment, serta term ID di luar dictionary snapshot. Simpan lineage/proof immutable
agar pemeriksaan ini tidak memerlukan lookup per record pada jalur query.

Refresh statistics, perubahan analyzer/formula, atau model incompatible membangun indeks
side-by-side. Dense bytes boleh dipakai ulang bila manifest dan inputnya identik, meskipun
sparse harus dihitung ulang. Snapshot lama tetap mengikat generation/collection lama.
Pilihan fisik ini dibekukan sesudah review kontrak dan eksperimen Qdrant; bila sharing tidak
dapat dibuktikan benar, implementasi wajib mengubah desain secara eksplisit dan melaporkan
biaya salinan/reindex, bukan diam-diam melemahkan filter snapshot.

### Batch, memori, dan kesalahan

Proses chunk dalam batch berbatas byte/token; jangan memuat seluruh corpus/vector ke RAM.
Pembangunan awal BM25 memerlukan pass pengumpulan terms/statistik lalu pass pembobotan;
gunakan shard artefak dan external merge bila batas memori tercapai. Reuse teks/vector
tetap memeriksa hak corpus dan provenance input, termasuk dependensi konteks induk.

Native embedding memakai purpose DOCUMENT, batas padded tokens dan admission runtime yang
sudah ada. Concurrency worker dan upsert dibatasi serta ditune dari profiling. Queue penuh,
deadline, item hilang, dan partial response dicatat. Tidak ada truncation diam-diam,
substitusi zero vector, atau publish sukses setelah sebagian chunk hilang.

## 4. Publication dan pembacaan konsisten

Urutan penulisan: validasi batch → reserve target/fence → simpan intent dan before-images →
dispatch batch backend → periksa status/durable acknowledgement → verifikasi data melalui
jalur baca → simpan receipt → coordinator melakukan CAS active snapshot. Membership sumber
dan model/generation diperiksa lagi sebelum commit. Proses ini menggunakan state machine S01.

Dispatcher mutasi mempunyai satu pemilik efektif per corpus. Lease PostgreSQL saja tidak
membatalkan write Qdrant yang sudah terkirim. Simpan identitas operasi serta kondisi
in-flight; hentikan dispatch ketika fence hilang, drain/reconcile sebelum takeover atau
kompensasi, dan tahan publication bila keheningan writer lama belum dapat dibuktikan.
Konfigurasi ordering/consistency Qdrant adalah bagian pin backend, bukan pengganti protokol
publication. [Sumber Qdrant](https://qdrant.tech/documentation/scaling/consistency-guarantees/)

Readiness tidak berasal dari jumlah point saja. Verifikasi manifest/konfigurasi collection,
seluruh expected point IDs/payload hash secara paginasi berbatas, counts/checksum operasi,
hasil penutupan visibility, dan query probe dense/sparse dengan filter produksi. ANN tidak
dianggap bisa membuktikan kehadiran setiap record melalui satu hasil top-k; keberadaan record
diperiksa lewat read/scroll yang tepat. Overhead readiness dilaporkan sebagai biaya publikasi.

Bukti read harus mencakup replica/routing/consistency policy yang benar-benar boleh dipakai
query sesudah publication, bukan hanya satu replica yang kebetulan sudah maju. Catat watermark
atau bukti ekuivalen yang didukung versi backend dan memenuhi manifest; jangan mengarang
watermark jika API tidak menyediakannya. Jika satu route masih tertinggal, tahan publication
atau pin seluruh query ke route yang telah diverifikasi dengan batas konfigurasi eksplisit.
Perubahan routing/replica invalidates proof terkait dan memerlukan pemeriksaan ulang sebelum
route baru melayani snapshot tersebut. Tambahkan fault test replica lag dan route switch.

Visibility memakai corpus + from_seq inclusive/to_seq exclusive + representation binding;
tanggal hukum adalah sumbu terpisah. Known/unknown/conflict mengikuti policy yang eksplisit.
Untuk chunk dengan beberapa versi pasal, pasangan version/interval/status tetap terikat.
Filter range yang mengambil awal dari versi A dan akhir dari versi B harus ditolak.
Nested payload dapat menjaga satu objek yang sama;
[dokumentasi Qdrant](https://qdrant.tech/documentation/search/filtering/) menjadi acuan operator,
lalu adapter mengujinya pada versi server terpin. Aturan semua bagian relevan pada chunk
tetap diverifikasi setelah hydration; jika perlu mengambil kandidat lanjut, budget dan
status PARTIAL harus eksplisit, tanpa mengklaim top-k cukup setelah membuang hasil.

Pada abort, hanya record target yang boleh di-retire dan closure dipulihkan menurut
before-image setelah writer lama aman. Snapshot berikutnya ditahan sampai recovery selesai.
Crash setelah CAS memulihkan job dari manifest committed dan tidak mengompensasi data committed.

## 5. Hubungan X01 dengan K01 dan Q01

Builder/index reader dapat diuji sekarang menggunakan CHUNK tervalidasi pada corpus uji.
Uji komponen tidak mensimulasikan keberhasilan RESOLVE/ASSEMBLE atau membuat receipt Neo4j
palsu. INGEST penuh tetap mensyaratkan output graph yang diperlukan sebelum publication
Hybrid GraphRAG; daemon tidak melompati stage yang belum selesai.

Sebelum menyediakan command REBUILD khusus indeks untuk snapshot yang sudah terpublikasi,
tetapkan admission eksplisit terhadap input/membership/capability snapshot dan backend yang
wajib memberi receipt. Operation enum REBUILD sudah ada; bentuk plan dan kontrak inputnya
perlu diikat saat audit C01. Snapshot yang hanya memenuhi pengujian indeks tidak diumumkan
sebagai graph-ready. Batas ini memungkinkan implementasi X01 maju tanpa mengubah makna
milestone K01 atau mengizinkan graph stale pada snapshot baru.

X01 menyediakan primitive pencarian nyata untuk dense/BM25 dan smoke CLI. Orkestrasi query,
entity linking, traversal graph, fusion, dan reranking lengkap tetap dikerjakan di Q01.
Pencarian smoke membawa snapshot, generation, raw score, chunk/version refs, dan durations
agar hasil bisa direproduksi serta kemudian dipakai evaluator produksi yang sama.

## 6. Matriks validasi sebelum gold lengkap

| Kelompok | Kasus penting | Expected outcome |
| --- | --- | --- |
| Input/provenance | Hash artefak salah; span bukan batas UTF-8; parent/versi hilang; corpus lain; membership snapshot tidak terbukti | Gagal sebelum inference/mutasi; alasan terstruktur dan tidak publish |
| Dense | Salah model/dimensi, NaN/Inf/zero/nonunit, hasil reordered/duplikat/hilang/partial, overlength | Korelasi tepat atau error; tidak menukar chunk dan tidak menyembunyikan item gagal |
| Analyzer | `Pasal 12 ayat (3)`, `12/2020` vs `120/2020`, `tidak wajib`, Unicode composed/decomposed, informal/code-switch | Go/Rust menghasilkan terms sesuai fixture; nomor/negasi tidak hilang |
| BM25 | Frozen stats, repeated terms, zero tokens, OOV, dictionary append, future/sibling revision, sparse index duplikat/tak urut | Score sparse setara referensi; reuse dictionary hanya ancestor yang cocok; invalid batch ditolak |
| Reuse/update | Input sama; hanya metadata berubah; parent context berubah; tokenizer/model/statistics berubah | Reuse hanya dependency identik; invalidation tepat; refresh sparse tidak memanggil dense ulang tanpa alasan |
| Identity | Point ID collision, operation key sama payload beda, duplicate chunk/version | Conflict eksplisit; record lama tidak tertimpa diam-diam |
| Visibility/legal | Seq sebelum/pada closure, corpus/model lain, tanggal batas, unknown/conflict, multi-interval | Satu snapshot/representation konsisten; tidak menggabungkan interval dari versi berbeda |
| Backend | Timeout setelah write diterima, response malformed, satu replica tertinggal, route switch, count benar tetapi point salah | Reconcile intent; setiap route query dibuktikan sesuai manifest atau publication/routing ditahan |
| Concurrency | Dua publisher, fence kedaluwarsa, write lama masih berjalan, query saat publish | Satu publisher efektif; query lama/new pin konsisten; takeover tidak berlomba dengan write lama |
| Crash/abort | Setelah upsert, setelah closure, sebelum/sesudah CAS, saat kompensasi | Parent tetap dapat dibaca; abort/replay idempotent; snapshot committed tidak di-rollback fisik |
| Integrasi nyata | CHUNK nyata → BGE native → IndexBatch → PostgreSQL/Qdrant → search/filter | ID, vector, sparse score, metadata, publication receipt dan hasil read cocok |
| Full vs incremental | Corpus/update sama, model/analyzer/dictionary/statistics terpin sama | State logis/score/membership setara setelah menormalisasi field audit nonsemantik |

Fixture relevance bukan gold kualitas hukum. Properti matematika, integritas data, isolation,
dan recovery dapat diperiksa sekarang. Recall semantik, nDCG gold, kualitas graph/jawaban,
serta perbandingan empat baseline tetap memerlukan G01 dan tidak diluluskan dari fixture.

## 7. Pengukuran, review, dan paket commit

Catat throughput chunk/token, embedding reuse/hit/miss/error, RSS/VRAM, bytes per batch,
write/read latency p50/p95/p99, queue wait, durasi readiness/publication, indeks/artefak bytes,
OOV/drift, serta biaya update versus refresh/rebuild. Ukur query bersamaan dengan ingestion.
Seluruh arrival/rejection/timeout masuk denominator. Profil lokal dicatat sebagai diagnostik.
Target numerik tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml), terutama
SEARCH.*, MODEL.*, INVARIANT.*, UPDATE.*, dan ISOLATION.*; quality dan workload eligibility
mengikuti [kebijakan benchmark](benchmark-policy.md).

Urutan paket implementasi/commit yang dapat ditinjau:

1. Kontrak artefak/compatibility generation, checksum, validators dan fixture lintas bahasa.
2. Analyzer/dictionary/statistik frozen serta sparse scoring parity.
3. Dense builder/reuse dan IndexBatch terikat provenance.
4. Adapter Qdrant nyata, point mapping, payload filters, search dan readiness.
5. Writer/ledger, publication, abort/recovery dan concurrency tests terhadap backend nyata.
6. INDEX worker/coordinator, lease renewal, CLI/config dan integrasi proses.
7. Profiling throughput/memori/latency, perbaikan bottleneck, dokumentasi serta laporan.

Reviewer terpisah digunakan setelah paket kontrak, batas storage/publication, dan integrasi
akhir koheren. Helper/refactor kecil cukup diperiksa implementer. Header fungsional dan
benchmark dipertahankan; README status dibatch pada akhir paket. Commit menjelaskan fitur
konkret seperti `feat(indexing): build versioned dense records from native embeddings`.

Laporan membedakan IMPLEMENTED/TESTED pada cakupan kode dari penerimaan kualitas/release.
Gold yang belum tersedia menghasilkan BLOCKED/NOT_MEASURED untuk gate yang memerlukannya.
Qdrant server/SDK dipilih, diverifikasi kompatibilitasnya, dan dipin sebelum integration run;
tidak ada nomor versi yang diasumsikan sudah teruji dalam rencana ini. Deployment tetap
di luar pekerjaan yang diotorisasi saat ini.
