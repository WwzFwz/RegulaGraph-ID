# doc

[verification-report-candidate-revalidation.md](verification-report-candidate-revalidation.md)
mencatat revalidasi konteks kandidat historis, lookup kosong dan perubahan profil/alias
sebagai salah satu prasyarat reuse hasil RESOLVE pada publication berikutnya.

[graph-assembly-coordinator.md](graph-assembly-coordinator.md) memetakan authority
receipt/checkpoint/publication Go serta urutan handoff snapshot dan registry lintas sumber.
[verification-report-assembly-authority.md](verification-report-assembly-authority.md)
menyimpan hasil PostgreSQL, regresi Go dan review independen gate awal tersebut.

[assembly-worker.md](assembly-worker.md) menjelaskan eksekusi plan graph oleh Rust,
batas input, hash/provenance, cancellation dan checkpoint sebelum publication Go.
[verification-report-assembly-worker.md](verification-report-assembly-worker.md)
mencatat tes storage/processor/service, review independen dan batas end-to-end.

[graph-assembly-inputs.md](graph-assembly-inputs.md) menjelaskan canonical selection
revision-bound, role plan ASSEMBLE dan kewajiban source/receipt/fence pemanggil.
[verification-report-graph-assembly-inputs.md](verification-report-graph-assembly-inputs.md)
mencatat uji PostgreSQL, wire empat bahasa, guard input dan review independen.

[graph-delta.md](graph-delta.md) menjelaskan assembly upsert, validasi teks sumber,
dependency/visibility dan kewajiban caller sebelum worker/publication disambungkan.
[verification-report-graph-delta.md](verification-report-graph-delta.md) mencatat
regresi source/UTF-8/identity, rerun independen serta batas bukti library.

[registry-history.md](registry-history.md) menjelaskan lookup historis di bawah lease,
binding publication immutable dan batas history corpus existing pada migration 0018.
[verification-report-registry-history.md](verification-report-registry-history.md)
mencatat upgrade, concurrent lease release dan regresi PostgreSQL independen.

[graph-canonical-identity.md](graph-canonical-identity.md) menjelaskan dedup assertion,
support provenance, exception remap dan batas library Rust prapublikasi.
[verification-report-graph-canonical.md](verification-report-graph-canonical.md)
mencatat pengujian serta temuan determinisme/identity yang sudah diperbaiki.

[semantic-review.md](semantic-review.md) menjelaskan inspeksi/acceptance operator
lokal, hash/revision binding dan resume tanpa model call ulang.
[verification-report-semantic-review.md](verification-report-semantic-review.md)
membedakan bukti DEFER end-to-end dan LINK transaction dari kualitas model.

[evidence-api.md](evidence-api.md) menjelaskan runtime HTTP lokal untuk query
evidence terpin, autentikasi, resource reuse, readiness dan cara menghentikannya.
[verification-report-evidence-api.md](verification-report-evidence-api.md)
mencatat native concurrency/HTTP lifecycle serta keterbatasan benchmark.

[verification-report-native-index.md](verification-report-native-index.md) mencatat
run Rust/Go/C++ dengan embedding BGE-M3 nyata sampai publication dan hybrid evidence,
termasuk temuan kompatibilitas media DocumentBatch dan batas fixture/gold.

[index-job-inventory.md](index-job-inventory.md) menjelaskan inventory/claim
durable INDEX dan integrasi executor; bukti storage terdapat
pada [verification-report-index-jobs.md](verification-report-index-jobs.md).
Integrasi processor/workflow/daemon dan lost acknowledgement dilacak pada
[verification-report-index-executor.md](verification-report-index-executor.md).

[verification-report-index-planning.md](verification-report-index-planning.md)
mencatat planner inventory, persistence plan, dispatch/admission worker dan integrasi
writer snapshot awal; scheduler durable dan statistik corpus masih tahap berikutnya.

[initial-index-writer.md](initial-index-writer.md) menjelaskan preparation sumber,
catalog/intent PostgreSQL, writer Qdrant awal, readback dan receipt publication.
[verification-report-initial-index.md](verification-report-initial-index.md)
mencatat bukti tes backend nyata dan batas integrasi yang masih terbuka.

[index-build.md](index-build.md) menetapkan plan INDEX, perakitan worker Rust,
admission Go, commitment vector, policy BM25, batas batch dan pekerjaan publication
yang masih terbuka.

[verification-report-index-build.md](verification-report-index-build.md) mencatat
hasil tes dan review independen worker INDEX, termasuk batas fixture serta pekerjaan
coordinator/publication yang belum terhubung.

Dokumentasi desain, keputusan arsitektur, kontrak data, dan referensi proyek. Folder ini menjelaskan sistem secara menyeluruh serta alasan pemilihan desain, tanpa menjalankan pipeline. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Kode produksi, dataset benchmark, dan hasil eksperimen berada di folder masing-masing. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Dokumen anak harus membedakan keputusan yang disepakati, hipotesis yang akan diuji, dan implementasi yang benar-benar tersedia. Tautkan perubahan kontrak ke komponen terdampak; pertahankan sumber referensi asli.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

[verification-report-artifact-identities.md](verification-report-artifact-identities.md)
mencatat perbaikan alamat fisik/logis batch worker pada admission INDEX dan hidrasi,
tes PostgreSQL/Qdrant sampai draft, review independen, dan batas terhadap acceptance.

[lexical-generation.md](lexical-generation.md) mendefinisikan artefak analyzer dan
statistik frozen, formula serta handoff Rust ke Go. Bukti parity, backend dan
batas kesiapannya ada pada [laporan](verification-report-lexical-generation.md).

[verification-report-rag-workflow.md](verification-report-rag-workflow.md)
mencatat integrasi library retrieval menuju draft bersitasi, uji branch pada
Qdrant nyata, perbaikan boundary hidrasi, dan koneksi storage/model yang belum siap.

[verification-report-x01-lexical-allocator.md](verification-report-x01-lexical-allocator.md)
mencatat migration dan tes PostgreSQL nyata untuk allocator term BM25 per revisi;
stage INDEX dan benchmark release masih belum selesai.
[verification-report-x01-dictionary-fingerprint.md](verification-report-x01-dictionary-fingerprint.md)
mencatat fixture byte fingerprint Go–Rust untuk mapping dictionary yang sama.

Subfolder yang dikelola: [decisions/](decisions/README.md) dan
[interview/](interview/README.md). Folder interview menghubungkan penjelasan
arsitektur lengkap, pilihan teknologi, flow, matematika dan peta kode dengan
asumsi seluruh komponen terintegrasi pada dokumen 01-06 dan contoh input/output
per tahap pada dokumen 08. Dokumen 09 membahas Evaluation, Reliability,
Observability, Scalability, dan System Design. Status demo, komponen
tersedia dan pekerjaan tersisa dipisahkan dalam dokumen 07.

Berkas langsung: [acquisition.md](acquisition.md), [system-design.md](system-design.md), [system-contracts.md](system-contracts.md), [storage-consistency.md](storage-consistency.md), [corpus-plan.md](corpus-plan.md), [development-plan.md](development-plan.md), [benchmark-targets.md](benchmark-targets.md), [evaluation-runner.md](evaluation-runner.md), [Graph-Engineering-Athropic-Playbook.pdf](Graph-Engineering-Athropic-Playbook.pdf), [architecture.md](architecture.md), [benchmark-policy.md](benchmark-policy.md), [data-model.md](data-model.md), [reference.md](reference.md), [runtime-language-review.md](runtime-language-review.md), [verification-report-d01-inventory.md](verification-report-d01-inventory.md), [verification-report-e01.md](verification-report-e01.md), [verification-report-i01-document-artifacts.md](verification-report-i01-document-artifacts.md), [verification-report-i01-durable-chunk.md](verification-report-i01-durable-chunk.md), [verification-report-i01-durable-extract.md](verification-report-i01-durable-extract.md), [verification-report-i01-durable-dispatch.md](verification-report-i01-durable-dispatch.md), [verification-report-i01-retry-schedule.md](verification-report-i01-retry-schedule.md), [verification-report-i01-source-provenance.md](verification-report-i01-source-provenance.md), [verification-report-i01-structure-handoff.md](verification-report-i01-structure-handoff.md), [verification-report-i01-terminal-outcome.md](verification-report-i01-terminal-outcome.md), [verification-report-i01-incremental-planning.md](verification-report-i01-incremental-planning.md), [verification-report-i01-pdf-parser.md](verification-report-i01-pdf-parser.md), [verification-report-i01-text-normalization.md](verification-report-i01-text-normalization.md), [verification-report-i01-structural-chunking.md](verification-report-i01-structural-chunking.md), [verification-report-i01-versioning.md](verification-report-i01-versioning.md), [verification-report-i01-worker-transport.md](verification-report-i01-worker-transport.md), [verification-report-k01-bind-executor.md](verification-report-k01-bind-executor.md), [verification-report-k01-document-binding.md](verification-report-k01-document-binding.md), [verification-report-k01-exact-registry.md](verification-report-k01-exact-registry.md), [verification-report-k01-stage-handoff.md](verification-report-k01-stage-handoff.md), [verification-report-m01-pdf-profile.md](verification-report-m01-pdf-profile.md), dan [verification-report-s01.md](verification-report-s01.md).

[verification-report-semantic-extract.md](verification-report-semantic-extract.md) merekam audit adversarial boundary model EXTRACT, sedangkan [verification-report-i01-durable-extract.md](verification-report-i01-durable-extract.md) merekam claim, commit, source-evidence validation, dan recovery coordinator EXTRACT. [verification-report-k01-alias-registry.md](verification-report-k01-alias-registry.md) mencatat bukti PostgreSQL untuk alias berversi, lookup kandidat ambigu, revision lookup kosong, serta batas hasil verifikasinya.

[verification-report-q01-a01-core.md](verification-report-q01-a01-core.md) mencatat fungsi fusion dan packing konteks yang diuji, beserta batas bahwa retrieval, generation, dan benchmark end-to-end belum aktif.

[verification-report-n01-a01-boundaries.md](verification-report-n01-a01-boundaries.md) mencatat scheduler native serta validasi struktural jawaban/sitasi. [verification-report-x01-bm25-statistics.md](verification-report-x01-bm25-statistics.md) mencatat statistik BM25 lokal dari token yang sudah dianalisis. [verification-report-q01-rerank-correlation.md](verification-report-q01-rerank-correlation.md) mencatat korelasi hasil reranker. Ketiga laporan membatasi PASS pada fixture fungsi terkait, bukan milestone atau benchmark end-to-end. [verification-report-g01-annotation-queue.md](verification-report-g01-annotation-queue.md) mencatat antrean PDF kandidat yang terikat inventory; G01 gold manusia belum selesai.

[verification-report-x01-lexical-library.md](verification-report-x01-lexical-library.md)
mencatat analyzer Rust–Go terpin, dictionary descendant lokal, bobot sparse BM25 frozen,
review independen, dan batas bahwa backend serta benchmark X01 belum aktif.
[verification-report-k01-sourced-alias.md](verification-report-k01-sourced-alias.md) mencatat materialisasi alias dari keputusan LINK registry yang terverifikasi sebagai helper lokal; stage RESOLVE belum selesai.

[verification-report-k01-q01-a01-library.md](verification-report-k01-q01-a01-library.md)
mencatat validasi endpoint canonical K01 prapublikasi, encoder sparse query BM25,
dan gate cakupan teks A01. Ketiganya adalah library lokal; jalur produksi penuh,
gold, dan benchmark release belum dibuktikan.
[verification-report-x01-paired-filters.md](verification-report-x01-paired-filters.md)
mencatat kontrak filter legal per versi, fixture lintas bahasa, gate sumber
terverifikasi, dan prasyarat rollout pembaca sebelum publication.
[verification-report-x01-qdrant-transport.md](verification-report-x01-qdrant-transport.md)
mencatat transport Qdrant v1.18 yang diuji via HTTP sintetis serta batas
readiness, backend hidup, dan benchmark yang masih terbuka.
[verification-report-x01-qdrant-readback.md](verification-report-x01-qdrant-readback.md)
mencatat perbandingan exact-ID atas payload serta vector Qdrant dan batas
pembuktiannya sebelum publication seluruh snapshot.
[verification-report-x01-index-batch.md](verification-report-x01-index-batch.md)
mencatat gate closure lokal IndexBatch dan prasyarat checksum, prior state,
serta fence sebelum mutation dan publication.
[verification-report-x01-dense-payload.md](verification-report-x01-dense-payload.md)
mencatat builder dense Rust berbatas dan gate indeks payload Qdrant bagi
filter snapshot; integrasi worker/publication dan benchmark belum dibuktikan.
[verification-report-x01-rendering-sparse.md](verification-report-x01-rendering-sparse.md)
mencatat renderer teks chunk dengan konteks induk dan penolakan urutan term
sparse yang tidak sah pada batch/Qdrant; pengikatan worker dan backend hidup
masih terbuka.
[verification-report-x01-verified-inputs.md](verification-report-x01-verified-inputs.md)
mencatat policy label node pemilik, loader TextArtifact terverifikasi, key reuse
embedding, dan batas bahwa worker INDEX maupun benchmark belum aktif.
[verification-report-x01-input-policy.md](verification-report-x01-input-policy.md)
mencatat tag policy generation aditif, binding lintas bahasa, admission
Go/Rust/Qdrant, refresh schema lock setelah review, serta syarat upgrade reader.
[verification-report-x01-provenance-items.md](verification-report-x01-provenance-items.md)
mencatat proyeksi source/version/page bersama EXTRACT dan input native INDEX,
uji adversarial, review independen, serta batas bahwa worker INDEX belum aktif.
[verification-report-k01-candidate-batch.md](verification-report-k01-candidate-batch.md) mencatat
builder kandidat deterministik dan validasi alias-to-scope; autentikasi PostgreSQL serta stage RESOLVE
masih pekerjaan lanjutan.
[verification-report-k01-pg-candidate-producer.md](verification-report-k01-pg-candidate-producer.md)
mencatat adapter baca batch registry dan batas verifikasinya sebelum workflow RESOLVE aktif.
[verification-report-k01-resolution-proposals.md](verification-report-k01-resolution-proposals.md)
mencatat proposal LINK/DEFER Rust dari pilihan eksplisit dan kandidat terpin; keputusan registry
serta stage RESOLVE belum tersedia.
[verification-report-k01-go-candidate-proposals.md](verification-report-k01-go-candidate-proposals.md)
mencatat pemeriksaan Go atas scope ID, alias kandidat positif, serta proposal sebelum keputusan
registry; receipt dan integrasi stage masih belum diuji.
[verification-report-k01-registry-receipts.md](verification-report-k01-registry-receipts.md)
mencatat validator struktural receipt LINK/DEFER terhadap kandidat terpin serta review independen;
autentikasi PostgreSQL, stage RESOLVE, dan akurasi hukum masih menunggu integrasi dan gold.
[verification-report-k01-resolution-assembly.md](verification-report-k01-resolution-assembly.md)
mencatat builder artefak RESOLVE yang membawa keputusan serta dependency EXTRACT/kandidat;
autentikasi byte artefak dan transaksi registry masih milik workflow/adapter.
[verification-report-k01-semantic-registry.md](verification-report-k01-semantic-registry.md)
mencatat writer PostgreSQL LINK/DEFER, review terikat proposal/kandidat, CAS, replay, serta
batas bahwa workflow RESOLVE dan penerimaan kualitas/performa belum aktif.
[verification-report-k01-resolve-handoff.md](verification-report-k01-resolve-handoff.md)
mencatat klaim EXTRACT→RESOLVE, byte `ReadVerified`, dan fencing transaksi registry yang diuji;
producer review terautentikasi dan executor RESOLVE penuh masih terbuka.
[verification-report-k01-resolve-output.md](verification-report-k01-resolve-output.md) mencatat intent sebelum CAS, output/checkpoint, pemulihan crash, dan jalur tanpa mention pada PostgreSQL disposable; producer dan benchmark produksi masih terbuka.
[verification-report-k01-resolve-replan.md](verification-report-k01-resolve-replan.md) mencatat terminal failure untuk kandidat/review stale dan prioritas cancellation/fence, dengan batas bahwa pembuatan job pengganti masih terbuka.
[verification-report-k01-candidate-storage.md](verification-report-k01-candidate-storage.md) mencatat handoff kandidat berfence, revision lookup, artefak berhash, dan dependency PostgreSQL; pemilihan legal scope serta producer proposal tetap terbuka.
[verification-report-g01-review-packet.md](verification-report-g01-review-packet.md) mencatat triase enam PDF
yang metadata antreannya dicocokkan ulang ke inventory dan byte PDF-nya diverifikasi; gold manusia belum tersedia.
[verification-report-a01-citation-mapping.md](verification-report-a01-citation-mapping.md) mencatat helper
sitasi sumber terikat versi, cakupan semua bukti klaim, pemeriksaan URL final, dan batas integrasinya.

Mulai dari system-design untuk membaca keseluruhan rancangan, lalu system-contracts dan storage-consistency untuk semantik integrasi. Corpus-plan mengikat sumber pilihan pengguna dan prosedur gold dataset. Development-plan mengurutkan seluruh implementasi menurut dependency beserta bukti kelulusannya. Dokumen desain tidak berarti pipeline atau benchmark sudah aktif; status kontrak C01 dijelaskan terpisah.

## Benchmark dan perhatian kualitas

**DOC.** Dokumentasi harus konsisten dengan status scaffold/implementasi dan tidak menyatakan hasil eksperimen yang belum dijalankan. Tautan lokal dan rujukan kontrak diperiksa.

Lihat [kebijakan benchmark](benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Collector dan audit inventory D01, kontrak/validator C01, evaluator offline E01, fondasi control-plane
storage/publication S01, source provenance, parser PDFium, normalizer teks, parser struktur hukum, parent-aware chunk builder, boundary artefak dokumen, incremental planner, selector timeline, serta worker/coordinator PARSE -> STRUCTURE -> BIND -> CHUNK -> EXTRACT I01 sudah aktif. Planner exact regulation identity, allocator canonical PostgreSQL, materializer `Regulation`/`ProvisionVersion`, workflow/persistence BIND, dan binding chunk per-node K01 juga aktif. Profiler M01 telah membandingkan tiga
engine PDF. Worker EXTRACT, Semantic Gateway, commit/recovery durable EXTRACT, serta ontology EXTRACT terpin bersama sudah aktif secara deterministik. Proposal resolusi kontekstual, planner kandidat terpin, dan CLI submit blob tersedia; keputusan canonical baru/merge-split serta dispatch RESOLVE produksi belum aktif. Stage ASSEMBLE-INDEX, graph/retrieval, writer publikasi Qdrant/Neo4j,
provider/model produksi, gold dataset, serta acceptance run produksi belum aktif. Primitive fusion, korelasi reranker,
statistik BM25 lokal, packing konteks, dan validasi struktural sitasi telah diuji, tetapi belum membentuk pipeline query produksi. Status anak dijelaskan pada header
masing-masing; profil parser, audit integrity, test correctness S01, dan evaluator sintetis tidak membuktikan
target kualitas atau latency produksi.

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [implementation-guide.md](implementation-guide.md), [contracts-implementation.md](contracts-implementation.md), [verification.md](verification.md), [verification-contracts.md](verification-contracts.md), [verification-pipeline.md](verification-pipeline.md), [verification-quality.md](verification-quality.md). Mulai dari verification.md untuk reviewer dan implementation-guide.md untuk implementer. Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](contracts-implementation.md).

Ringkasan milestone: [verification-report-c01.md](verification-report-c01.md),
[verification-report-d01-inventory.md](verification-report-d01-inventory.md),
[verification-report-e01.md](verification-report-e01.md),
[verification-report-i01-document-artifacts.md](verification-report-i01-document-artifacts.md),
[verification-report-i01-durable-chunk.md](verification-report-i01-durable-chunk.md),
[verification-report-i01-durable-extract.md](verification-report-i01-durable-extract.md),
[verification-report-i01-durable-dispatch.md](verification-report-i01-durable-dispatch.md),
[verification-report-i01-retry-schedule.md](verification-report-i01-retry-schedule.md),
[verification-report-i01-source-provenance.md](verification-report-i01-source-provenance.md),
[verification-report-i01-structure-handoff.md](verification-report-i01-structure-handoff.md),
[verification-report-i01-terminal-outcome.md](verification-report-i01-terminal-outcome.md),
[verification-report-i01-incremental-planning.md](verification-report-i01-incremental-planning.md),
[verification-report-i01-pdf-parser.md](verification-report-i01-pdf-parser.md),
[verification-report-i01-text-normalization.md](verification-report-i01-text-normalization.md),
[verification-report-i01-structural-chunking.md](verification-report-i01-structural-chunking.md),
[verification-report-i01-versioning.md](verification-report-i01-versioning.md),
[verification-report-i01-worker-transport.md](verification-report-i01-worker-transport.md),
[verification-report-k01-bind-executor.md](verification-report-k01-bind-executor.md),
[verification-report-k01-document-binding.md](verification-report-k01-document-binding.md),
[verification-report-k01-exact-registry.md](verification-report-k01-exact-registry.md),
[verification-report-k01-stage-handoff.md](verification-report-k01-stage-handoff.md),
[verification-report-k01-ontology.md](verification-report-k01-ontology.md),
[verification-report-k01-resolution-closure.md](verification-report-k01-resolution-closure.md),
[verification-report-m01-pdf-profile.md](verification-report-m01-pdf-profile.md), dan
[verification-report-s01.md](verification-report-s01.md), serta
[verification-report-semantic-extract.md](verification-report-semantic-extract.md). Laporan S01 mengikat implementasi
storage/publication ke PostgreSQL aktual dan audit agent independen.

[semantic-resolution.md](semantic-resolution.md) menjelaskan gateway model kontekstual, workflow audit/replay, setup endpoint lokal, dan batas dispatch/approval. [verification-report-contextual-resolution.md](verification-report-contextual-resolution.md) mencatat bukti correctness lintas runtime dan prasyarat kualitas yang belum diukur.

[verification-report-candidate-evidence.md](verification-report-candidate-evidence.md) mencatat katalog checkpoint EXTRACT atomik, hidrasi bukti kandidat lintas dokumen, citation kedua sisi LINK, dan smoke protokol model lokal. Status tersebut belum menyelesaikan dispatch RESOLVE, K01, ataupun acceptance kualitas/performa.

[verification-report-candidate-planner.md](verification-report-candidate-planner.md) mencatat policy kandidat terpin pada request ingest, parity normalisasi Go/Rust, cakupan tipe ontology, isolasi scope pasal pada PostgreSQL, dan batas pengukuran kualitas/performa yang masih terbuka. [verification-report-policy-submit.md](verification-report-policy-submit.md) mencatat loader policy, CLI submit idempotent, verifikasi pin terhadap request durable, dan hasil PostgreSQL nyata; dispatch RESOLVE serta kualitas model masih terbuka.

[verification-report-resolve-executor.md](verification-report-resolve-executor.md) melanjutkan paket tersebut dengan dispatch RESOLVE opt-in, pin producer, dan park proposal atomik ke WAITING_REVIEW. Review/resume terautentikasi, keputusan identitas baru, serta acceptance kualitas/performa tetap terbuka; paket ini belum menyelesaikan K01.

[native-inference.md](native-inference.md) menjelaskan bundle/model/runtime serta reproduksi lokal; [verification-report-native-models.md](verification-report-native-models.md) mencatat tes lintas bahasa, parity model nyata dan batas penerimaan M01/N01.

[x01-implementation-plan.md](x01-implementation-plan.md) merencanakan komponen/fungsi indeks dense dan BM25, kontrak generation, publication/recovery, serta validasi sebelum gold lengkap. Dokumen ini menjadi handoff implementasi berikutnya dan tidak mengklaim fungsi yang direncanakan telah aktif.

[k01-implementation-plan.md](k01-implementation-plan.md) memecah review/resume resolusi,
identitas canonical, assembly graph, dan publication Neo4j.
[q01-implementation-plan.md](q01-implementation-plan.md) menyambungkan query planning,
pencarian dense/BM25/graph, reranking, dan hidrasi bukti pada satu snapshot.
[a01-implementation-plan.md](a01-implementation-plan.md) merencanakan konteks bersumber,
generator, validasi klaim/citation, dan streaming. Ketiganya menjelaskan fungsi usulan,
kontrak, validasi serta batas penerimaan; status implementasi tidak berubah hanya
karena rencana ini tersedia.

[verification-report-implementation-plans.md](verification-report-implementation-plans.md)
mencatat temuan audit independen pada batas indeks, graph, retrieval dan jawaban,
perbaikannya, serta status pemeriksaan dokumentasi tanpa mengklaim acceptance kode.

[verification-report-x01-dictionary-artifact.md](verification-report-x01-dictionary-artifact.md)
mencatat handoff dictionary Go/Rust, exporter PostgreSQL terpin, uji empat bahasa,
review independen dan batas integrasi serta benchmark yang masih terbuka.

[pinned-evidence.md](pinned-evidence.md) mendokumentasikan reader katalog,
hidrasi teks sumber, kebijakan AS_OF dan kepemilikan lease request sampai draft.
[verification-report-pinned-evidence.md](verification-report-pinned-evidence.md)
memisahkan bukti integrasi storage nyata dari model sintetis serta gate yang
belum diukur.

[query-evidence.md](query-evidence.md) menjelaskan CLI operator untuk pencarian
bukti terpin serta factory query berbasis katalog. [Laporan verifikasi query](verification-report-query-evidence.md)
memisahkan tes storage nyata, provider sintetis, dan target yang belum diukur.
[interview-demo.md](interview-demo.md) adalah panduan menjalankan baseline PDF RAG
lokal. [verification-report-interview-demo.md](verification-report-interview-demo.md)
mencatat tes serta batas klaimnya; ini tidak menutup milestone produksi.

[verification-report-evidence-reranking.md](verification-report-evidence-reranking.md)
mencatat integrasi reranker terpin setelah hidrasi, pengujian storage nyata dengan
model sintetis, dan smoke reranker BGE melalui C++ native. Hasil tersebut tetap
dipisahkan dari kualitas gold dan target benchmark required.

[lexical-population.md](lexical-population.md) menjelaskan persiapan vocabulary,
allocator PostgreSQL dan statistik BM25 dari rendered chunk.
[verification-report-lexical-population.md](verification-report-lexical-population.md)
memisahkan tes artifact/interop dari acceptance corpus dan benchmark.

[index-source-publication.md](index-source-publication.md) menjelaskan source envelope, recovery output dan perintah publication dense/BM25. Bukti reviewer/backend nyata serta keterbatasannya dicatat pada [verification-report-source-publication.md](verification-report-source-publication.md).
