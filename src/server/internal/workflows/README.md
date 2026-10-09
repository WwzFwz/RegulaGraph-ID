# src/server/internal/workflows

Preparation graph kini memanggil `VerifyDocumentRegistryView` sebelum ekspor/write.
Tes `document_registry_integration_test.go` memakai binder/allocator produksi dan
PostgreSQL terisolasi untuk reuse BIND; tidak mengklaim scheduler BIND atau model
berjalan dalam fixture. Alias/candidate freshness tetap gate terpisah.

`graph_resolution_receipt.go` membaca RESOLVE asli dan menuntut rekonstruksi exact
dari intent/kandidat/ledger committed; jalur mention-free tidak mengarang operasi.
`graph_assembly_prepare.go` mempersist plan/view deterministik dari graph source
receipt, dengan ontology pin, canonical selection, worker byte budget dan final
authority checks. DEFER ditahan sebelum export/write; freshness lintas revision
dan scheduling tidak tersirat oleh hasil preparation. Lihat
[kontrak input ASSEMBLE](../../../../doc/graph-assembly-inputs.md).

`graph_source_envelope_test.go` menguji handoff CHUNK/EXTRACT/RESOLVE melalui helper
domain, termasuk preservation model/assignment, dependency, remap diagnostic root dan
replay. `graph_source_binding.go` menyimpan envelope dan dependency immutable lalu
meminta receipt transactional dengan pemeriksaan ulang authority. Retry setelah
interruption/lost acknowledgement memakai artefak yang sama. Scheduling ASSEMBLE
belum tersedia; receipt transform tidak menggantikan receipt keputusan atau freshness
registry. Lihat [kontrak coordinator](../../../../doc/graph-assembly-coordinator.md).

Orchestration alur ingestion, pembaruan incremental, dan tanya jawab. Folder ini mengatur urutan tahap, percabangan, checkpoint, retry, dan pelaporan status. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menjadi tempat implementasi parser, retriever, atau SDK database. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menyuntikkan dependency melalui kontrak yang eksplisit dan membawa run ID serta snapshot ID. Retry harus idempotent, publikasi lintas penyimpanan memakai mekanisme yang dirancang, dan kegagalan parsial tetap terlihat.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[semantic_review.go](semantic_review.go) menyediakan inspeksi terverifikasi dan
acceptance batch LINK/DEFER operator lokal. Approval/intent/resume disimpan atomik;
executor melanjutkan tanpa inference ulang. Lihat [kontrak review](../../../../doc/semantic-review.md)
untuk batas principal, hash/revision, replay dan kualitas yang belum diukur.

[index.go](index.go) memiliki lifecycle claim/deadline/cancellation/retry job
INDEX melalui interface processor. Processor indexing meng-commit output; workflow
tidak menduplikasi algoritma atau writer. Success berarti STAGED durable, belum
published. Lost acknowledgement direkonsiliasi melalui exact checkpoint di storage.

Berkas: [answer.go](answer.go), [bind.go](bind.go), [bind_test.go](bind_test.go), [ingest.go](ingest.go), [ingest_test.go](ingest_test.go), [update.go](update.go), [parse.go](parse.go), [parse_test.go](parse_test.go), [collect.go](collect.go), [collect_test.go](collect_test.go), [discover.go](discover.go), [discover_test.go](discover_test.go), [semantic_resolution.go](semantic_resolution.go), dan [semantic_resolution_test.go](semantic_resolution_test.go).

collect menjalankan batch acquisition D01 melalui adapter sources dengan deduplikasi URL, jumlah worker terbatas, cancellation, progress, serta hitungan sukses/reuse/gagal. Scheduler pada ingest.go membuat job persisten S01 dan memilih `PARSE` bila semua source sudah berupa blob; URL tetap dimiliki `ACQUIRE`. `parse.go` merotasi claim PARSE/STRUCTURE/CHUNK/EXTRACT, hanya mengirim setiap tahap dari checkpoint pendahulunya yang sukses, membatasi RPC pada deadline lease, meneruskan cancellation durable, membaca ulang bytes output terverifikasi, menyimpan dependency manifest, lalu menyimpan checkpoint. Pada PARSE, observation hanya diteruskan bila corpus, portal, dan source-blob hash cocok dengan locator request. Hasil parsial masuk `WAITING_REVIEW`; hasil lengkap bergerak melalui handoff berikutnya. [Panduan collector](../../../../doc/acquisition.md) menjelaskan acquisition.

discover.go menyimpan checkpoint discovery.json dan antrean queue.txt setelah setiap halaman baru; satu proses penulis per direktori. Seed diikuti breadth-first dengan batas halaman per run, URL dideduplikasi, error dipertahankan dan dicoba ulang pada run berikutnya. Checkpoint adalah inventaris D01 lokal, bukan durable job produksi. Hasil discover tidak membuktikan ketersediaan PDF atau canonical identity.

`SemanticResolutionHandoff` menerima lease RESOLVE yang sudah diklaim, membandingkan checkpoint sukses EXTRACT dan metadata artefak kandidat terdaftar, lalu membaca kedua byte lewat `ReadVerified` dalam budget gabungan sebelum memanggil writer PostgreSQL dengan fence. Writer memverifikasi ulang lease/checkpoint pada transaksi CAS. [semantic_resolution_output.go](semantic_resolution_output.go) memeriksa batch dan menyimpan intent immutable sebelum CAS; setelah receipt cocok, ia menyimpan `ResolutionBatch`, dependency manifest, checkpoint terminal, dan menyelesaikan attempt. Retry setelah crash memakai intent yang sama; kandidat/review permanen stale membuat job `FAILED` agar rencana baru dapat dibuat dengan job baru. Cancellation atau lease yang kalah tidak dilaporkan sebagai permintaan replan. [semantic_resolution_empty.go](semantic_resolution_empty.go) menangani EXTRACT lengkap tanpa mention tanpa membuat keputusan registry. Handoff kandidat dan proposal model tersedia sebagai metode terpisah yang kini dipanggil SemanticExecutor; review/resume operator lokal tersedia melalui semantic_review.go.

[semantic_resolution_candidates.go](semantic_resolution_candidates.go) membuat dan menyimpan kandidat dari rencana scope exact caller atau `CandidatePlanningPolicy` corpus. Jalur policy menuntut hash yang sama pada konfigurasi handoff tepercaya, request ingest durable, dan manifest kandidat, serta fingerprint konfigurasi EXTRACT yang sama dengan request. Ia memeriksa lease/checkpoint dan perubahan revision di sekitar lookup; artefak berhash serta dependency manifest terdaftar sebelum dipakai writer. Scheduler/CLI submit kini mewajibkan policy corpus; dispatch proposal RESOLVE opt-in tersedia; identitas operator lokal dan resume job kini tersambung. Batas total scope policy harus sesuai kapasitas lookup unik; batas referensi runtime minimal mencakup satu dependency sumber, seluruh mention, scope, dan revision. Kandidat/alias tambahan masih dapat memerlukan kapasitas lebih besar berdasarkan data.

## Benchmark dan perhatian performa

[semantic_resolution_executor.go](semantic_resolution_executor.go) menyatukan claim, pin konfigurasi, planner dan proposal model. Output nonempty diparkir atomik sebagai `WAITING_REVIEW`; jalur kosong dan intent/checkpoint lama dipulihkan tanpa mengubah otorisasi sumber. Cancellation dipoll saat inference, waktu attempt dibatasi lease, dan retry memakai backoff. [semantic_resolution_executor_test.go](semantic_resolution_executor_test.go) menguji replay setelah crash, drift pin/auth, recovery kosong, budget registry agregat, serta cancellation/provider failure. CLI/daemon memakai [panduan konfigurasi](../../../../doc/semantic-resolution.md); review/resume operator lokal tersedia pada semantic-review.md; RBAC remote belum tersedia.

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

**ANSWER.** Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

`parse.go` memeriksa ontology hash pada request EXTRACT tersimpan dan manifest producer, lalu menerapkan gate typed graph setelah closure sumber/bukti. Mismatch menjadi kegagalan deterministik sebelum registrasi artefak.

Collector D01, kontrak/validator C01, scheduler durable S01, dispatch PARSE/STRUCTURE/CHUNK/EXTRACT ke worker Rust, dan executor BIND Go sudah aktif. BIND memverifikasi artefak/checkpoint STRUCTURE, menjalankan exact registry issuer/regulasi/pasal dalam batch idempotent, menulis dependency manifest dan output immutable, lalu memisahkan hasil lengkap dari review parsial. CHUNK memeriksa stage-owned records, completeness, typed reference closure, serta containment span terhadap versi dan struktur. EXTRACT hanya menerima CHUNK lengkap, mengikat model/prompt ke config request, memeriksa accounting/provenance, serta menghidrasi evidence text berbatas untuk exact mention dan boundary UTF-8 sebelum commit. Kegagalan baca storage sementara tetap `RETRY_WAIT`; korupsi immutable dan closure invalid menjadi terminal. Retry transient memakai exponential backoff durable dengan budget terpisah per stage; attempt global dan fence tetap monotonik. Klaim, fenced commit, output/checkpoint, dan recovery RESOLVE tersedia sebagai workflow yang menerima kandidat/proposal/review terpin; review/resume remote, lease renewal batch panjang, jitter retry, answering, dan benchmark end-to-end masih mengikuti paket berikutnya.

Recovery checkpoint otomatis berlaku untuk PARSE, STRUCTURE, CHUNK, dan EXTRACT karena checkpoint mengikat artefak, hash, fence, serta terminal outcome worker. Coordinator membaca ulang payload dan memulihkan dependency evidence sebelum menyelesaikan attempt. Lease yang diambil ulang menyalin checkpoint ke fence baru lalu meneruskan `SUCCEEDED`, `FAILED`, atau `CANCELLED` ke state yang sesuai tanpa menjalankan worker kembali. Checkpoint lama tanpa terminal outcome tidak ditebak dan hanya dapat diulang bila budget attempt masih tersedia.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answer.go](answer.go) | Pin one snapshot, resolve temporal intent, coordinate retrieval/context/generation and validate terminal evidence; propagate cancellation. | Test unavailable dependencies, evidence conflicts and snapshot rollover mid-request; trace queue and stage durations. |
| [collect.go](collect.go) | Retain resumable acquisition; integrate receipts into S01 jobs without changing PDF byte-cap accounting. | Test cancellation and budget stop with concurrent workers; reconcile completed/failed/deferred counts and no dangling producer. |
| [discover.go](discover.go) | Retain durable queue checkpoints; add source-specific discovery only after inspecting real portal layouts. | Test cycles, duplicate seeds, resumed pagination and bounded new-page counts; report coverage gaps explicitly. |
| [ingest.go](ingest.go), [parse.go](parse.go), dan [bind.go](bind.go) | Pertahankan dispatch/retry durable PARSE/STRUCTURE/BIND/CHUNK/EXTRACT; tambahkan lease heartbeat, RESOLVE, dan publication coordinator. | Injeksi crash di register/dependency/checkpoint/completion, uji cancellation dan bounded-capacity dispatch dengan PostgreSQL nyata, lalu ukur queue/call p95/p99. |
| [update.go](update.go) | Bangun dependency closure termasuk empty lookup revision; stage replacement dan pertahankan versi/bukti bersama. | Uji source withdrawal, late reference, canonical merge/split dan interrupted reindex; bandingkan dengan clean rebuild. |

[semantic_resolution_model.go](semantic_resolution_model.go) menyediakan `ProposeWithModel`: hydrate konteks dari EXTRACT/document/text terverifikasi, panggil gateway model, validasi proposal/revision, dan simpan request-response immutable untuk replay lintas restart. [semantic_resolution_evidence.go](semantic_resolution_evidence.go) mengambil seluruh support alias kandidat dari katalog checkpoint EXTRACT sukses dalam corpus/snapshot/auth yang sama, lalu memverifikasi ulang bytes serta closure dokumen. Cache teks/budget dipakai bersama dan dependency identik dideduplikasi; LINK wajib merujuk konteks mention serta kandidat terpilih. Hasilnya dapat diteruskan ke handoff registry yang sudah ada; metode ini tidak mengautentikasi reviewer; SemanticExecutor menggunakannya untuk dispatch proposal otomatis. Lihat [integrasi resolusi](../../../../doc/semantic-resolution.md).

`parse.go` memakai `SaveExtractionCheckpoint` untuk output EXTRACT yang telah divalidasi, termasuk recovery. Checkpoint sukses dan katalog mention disimpan dalam satu transaksi fenced. Output parsial/gagal tetap menyimpan outcome tanpa memasukkan lokasi bukti sebagai sumber kandidat.

[retrieval.go](retrieval.go) mengorkestrasi candidate search Vector RAG atau
Hybrid RAG dengan cabang dense/BM25 paralel, explicit failure, dan fusion bersama.
Graph profiles ditolak sebelum I/O. Callback branch wajib menghormati context;
workflow membatalkan dan menunggu sibling selesai sebelum mengembalikan error.
[rag.go](rag.go) menyambungkan search, port hidrasi tepercaya, dan
[answer.go](answer.go) untuk context serta draft bersitasi. Workflow menerima
snapshot yang sudah diotorisasi/dipin oleh caller; tidak menganggap supplied
RequestContext sebagai bukti akses. Saat ini hanya AS_OF dengan tanggal
eksplisit dan respons non-streaming yang diterima.

Hidrasi harus membaca hash sumber, membuktikan membership/versi/tanggal, dan
prefetch URL. Boundary menuntut accounting setiap kandidat, menolak versi lain,
memulihkan urutan/provenance ranking asli, serta memberikan salinan mendalam
hasil search kepada callback agar ekspektasi tidak bisa diubah.
[rag_test.go](rag_test.go) menguji komposisi sintetis sampai citation;
[retrieval_test.go](retrieval_test.go) menguji paralelisme, cancellation dan
konsistensi identitas. [rag_session.go](rag_session.go) kini memiliki lease
snapshot aktif sampai generation dan cleanup; [rag_hydration.go](rag_hydration.go)
memasang hydrator storage ke urutan hasil fusion. Factory milik caller wajib
memakai binding katalog dan client/model reusable, dengan akses corpus yang
sudah diautentikasi. [rag_session_test.go](rag_session_test.go) menguji lifecycle,
cancellation dan error. Reranker opsional terpin kini berjalan setelah hidrasi dan sebelum konteks pada
jalur evidence-only maupun answering. Kegagalannya menghentikan generation.
Tokenizer generator nyata dan API/CLI jawaban masih belum tersambung; CLI
evidence-only sudah tersedia. Lihat [kontrak](../../../../doc/pinned-evidence.md).

[published_query.go](published_query.go) mempersiapkan dependency dari binding
katalog: origin credential harus cocok persis, admission Qdrant hanya membaca,
dan BM25 memverifikasi registry/hash artefak. PreparedQuery.Bind menggunakan ulang
resource generation serta membuat hydrator per pin. Perubahan binding ditolak
sampai resource baru disiapkan. SearchQuestion/SearchPinnedQuestion menyediakan
jalur evidence-only yang sama dengan answering tanpa pemanggilan generator.
Lihat [panduan](../../../../doc/query-evidence.md) untuk lifecycle cold CLI/warm library.
`preview.go` menghubungkan BM25 sampel dengan generator opsional untuk interview.
Satu generation berjalan sekaligus; kegagalan model mempertahankan evidence dengan
status generation_failed. Tidak ada snapshot publication atau klaim applicability
hukum pada jalur ini. Entrypoint-nya CLI `demo`, bukan query produksi.
