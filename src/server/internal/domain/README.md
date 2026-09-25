# src/server/internal/domain

Definisi data dan invariant lintas komponen: dokumen, versi pasal, chunk, canonical entity, relasi, bukti, dan jawaban. Domain menjadi bahasa bersama kedua alur ingestion dan tanya jawab. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

[resolution_context.go](resolution_context.go) memvalidasi konteks mention dan support kandidat, tipe/corpus/revision, exact UTF-8 span, serta citation dua sisi untuk LINK. Gate yang sama dipakai gateway dan workflow return boundary; autentikasi alias dan hash sumber tetap milik workflow/storage. `ValidateExtractionBatchClosure` juga mengikat auth scope dan snapshot EXTRACT ke DocumentBatch; reuse lintas snapshot memerlukan bukti membership yang belum tersedia. Fixture adversarial memeriksa kontrak ini, bukan akurasi semantik.

Tidak mengandung client database, prompt model, routing HTTP, atau orchestration. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Semua anak memakai ID stabil, schema version, dan referensi sumber yang eksplisit. Kontrak tidak mengimpor SDK eksternal; perubahan kontrak harus ditinjau terhadap seluruh konsumennya. Sumber wire schema adalah src/contracts/proto; representasi Go tidak membuat schema paralel. Offset wire mengikuti byte UTF-8 end-exclusive.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[candidate_planning.go](candidate_planning.go) menurunkan scope lookup dari setiap mention EXTRACT lengkap berdasarkan policy corpus terpin dan regulation ID sumber yang diizinkan. Ia menormalisasi alias dengan casing Unicode kontekstual yang sama dengan Rust, mempertahankan seluruh scope termasuk yang ambigu, dan menolak overflow tanpa truncation. `Fingerprint` mengikat peta scope dan limit; workflow wajib memeriksa hash itu pada request ingest durable serta manifest kandidat. [candidate_planning_test.go](candidate_planning_test.go) menguji cakupan scope, drift policy, batas, dan parity key. Kode tipe kanonik pada [registry.go](registry.go) meliputi seluruh ontology v1 dengan ID lama yang tetap stabil; kualitas blocking pada gold dan latency masih perlu diukur.

[ontology.go](ontology.go) memuat vocabulary EXTRACT bersama dari JSONC terpin hash dan memvalidasi tipe mention, predicate, endpoint, origin, serta qualifier. [ontology_test.go](ontology_test.go) menutup drift istilah dan konfigurasi; gate deterministik ini masih membutuhkan gold set untuk mengukur kebenaran hukum.

Berkas: [answers.go](answers.go), [chunks.go](chunks.go), [documents.go](documents.go), [document_validation.go](document_validation.go), [extraction_validation.go](extraction_validation.go), [entities.go](entities.go), [evidence.go](evidence.go), [relations.go](relations.go), [registry.go](registry.go), serta [operations.go](operations.go) untuk boundary job/publication S01. Validator dokumen dan ekstraksi memeriksa closure referensi, accounting, provenance source/version, identitas model/prompt, qualifier provisional, serta containment span sebelum artefak diregistrasi. [registry_test.go](registry_test.go) memverifikasi exact-key planning, sedangkan [documents_test.go](documents_test.go) memverifikasi binding regulation/provision, provenance, completeness, dan structural closure. Validator wire dan boundary lintas record dijelaskan pada bagian C01 di bawah.

Validator `RegistryCandidateBatch` memeriksa coverage mention, konteks/sumber, hasil dan revisi per scope, scope ID length-prefixed yang sama dengan adapter PostgreSQL/Rust, alias bersumber untuk setiap kandidat positif, tipe/scope kandidat, serta batas referensi bersarang. Producer tetap harus memakai snapshot registry tepercaya; input eksternal harus dibatasi dengan `DecodeWire` sebelum persist atau kerja mahal. Hasil lookup PostgreSQL dan akurasi kandidat belum dibuktikan oleh validator struktural ini.
[index_filters.go](index_filters.go) mewajibkan format paired dan policy
input embedding `structure-labels-v1` pada generation baru,
kesamaan visibility, penutupan setiap ID versi dalam record, dan menolak pencampuran
array legacy. [index_source_view.go](index_source_view.go) memvalidasi satu
DocumentBatch lengkap, menyalin fakta versi/chunk, lalu membandingkan setiap
filter record dengan interval/status/regulasi/blob/yurisdiksi sumber. Hash
artefak, membership snapshot, pembaca lama, dan writer Qdrant tetap tanggung
jawab X01/S01; fungsi lokal ini belum menerbitkan generation.
[index_batch.go](index_batch.go) kini memeriksa closure batch lokal: corpus,
generation, hitungan tanpa reject tersembunyi, identitas record/chunk unik,
setiap record terhadap sumber, target sequence tunggal, kecocokan model/vector,
dan konflik upsert-closure. Ia tidak
mengautentikasi checksum operasi, prior state closure, hash artefak, atau fence;
coordinator wajib memeriksa semua itu sebelum mutation/publication.

[candidate_batch.go](candidate_batch.go) membentuk batch kandidat deterministik dari observasi lookup yang
telah diambil pada satu revisi. Ia mengikat dependency EXTRACT, menurunkan revisi scope termasuk hasil
kosong, membatasi jumlah/byte sebelum clone, serta memvalidasi closure dan wire output. Pemilihan
scope hukum, autentikasi hasil lookup PostgreSQL, dan dispatch RESOLVE tetap tanggung jawab workflow
dan adapter terkait. [candidate_batch_test.go](candidate_batch_test.go) menguji permutasi, negative lookup,
revisi yang bertentangan, dan input terlalu besar tanpa mengklaim kualitas kandidat.
[resolution_candidates.go](resolution_candidates.go) memeriksa proposal LINK/DEFER sebelum keputusan
registry: setiap mention harus terwakili, LINK hanya menunjuk kandidat dalam lookup mention
yang sama pada revisi yang sama, dan DEFER mempertahankan seluruh kandidat ambigu. Bukti span
dan source version harus berasal dari mention EXTRACT. Ini belum memverifikasi receipt registry,
keputusan otoritatif, atau kebenaran identitas hukum; workflow wajib memanggilnya sebelum writer.
[resolution_candidates_test.go](resolution_candidates_test.go) menutup target asing, rev stale,
bukti palsu, dan kandidat tersembunyi. Kualitas/performa tetap REQUIRED_UNMEASURED.
[resolution_receipts.go](resolution_receipts.go) memeriksa request/response registry terhadap proposal
dan kandidat terpin: setiap proposal memiliki tepat satu keputusan LINK/DEFER, correlation ID,
revision, canonical ID, dan ID record tidak boleh bergeser atau bertabrakan. Error item dan
receipt parsial ditolak. [resolution_receipts_test.go](resolution_receipts_test.go) menguji drift
tersebut; validator ini belum mengautentikasi adapter/receipt PostgreSQL atau mengaktifkan stage
RESOLVE. Writer dan workflow wajib memakai keputusan terverifikasi dari transaksi otoritatif.
[resolution_batch_builder.go](resolution_batch_builder.go) merakit artefak RESOLVE immutable setelah
receipt lolos: ia mengikat hash EXTRACT/kandidat, revision lookup positif/negatif, model RESOLVE,
accounting mention, keputusan registry, serta konteks request. [resolution_batch_builder_test.go](resolution_batch_builder_test.go)
memeriksa dependency, copy input, receipt parsial, dan benturan ID. Caller masih wajib
memverifikasi byte artefak kandidat dan transaksi PostgreSQL; builder tidak mengaktifkan
workflow RESOLVE atau membuktikan kualitas LINK.

[semantic_resolution_receipt.go](semantic_resolution_receipt.go) membentuk preview keputusan deterministik yang dipakai sebelum CAS dan saat replay PostgreSQL. Preview bukan pengganti validasi input, autentikasi reviewer, atau receipt dari transaksi; kesetaraan preview dan hasil writer wajib diperiksa oleh workflow. Intent dalam [semantic_registry.go](semantic_registry.go) menyatakan request, approval, kandidat, dan batch yang tidak boleh berubah sepanjang retry job.
[semantic_candidates.go](semantic_candidates.go) mendefinisikan key lookup exact dan rencana scope per mention yang dibagi workflow serta PostgreSQL. Kebijakan pemilihan scope/normalisasi harus dipin dan diukur pada gold; tipe ini tidak memutuskan canonical identity.

[semantic_registry.go](semantic_registry.go) mendefinisikan input byte/ref, bukti lease/checkpoint,
dan assertion review lintas workflow Go serta adapter PostgreSQL. Tipe ini tidak dengan sendirinya
mengautentikasi aktor, mengesahkan isi hukum, atau membuka koneksi; writer yang memeriksa bukti
lease dan review secara transaksional. Handoff memakai budget byte dan target performa dari
konfigurasi serta `configs/benchmark-targets.yaml`.

`VerifyCitationEvidence` pada [boundaries.go](boundaries.go) kini menolak ID klaim/sitasi duplikat,
memerlukan sitasi untuk setiap pasangan klaim–evidence–source version pada klaim SUPPORTED,
serta memakai satu lookup URL tepercaya per source/version selama validasi. Evidence multisumber
wajib memiliki page locator terikat blob; kontrak saat ini tidak membedakan mirror alternatif
dari dukungan bersama, sehingga gate memilih kebijakan fail-closed sampai kontrak diperluas.

## Benchmark dan perhatian performa

**DOMAIN.** Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Kontrak/validator C01, tipe boundary job/publication S01, planner exact regulation identity K01, serta transform assignment registry menjadi `Regulation`, `DocumentEdition`, `Provision`, dan `ProvisionVersion` sudah aktif; lihat [cakupan implementasi C01](../../../../doc/contracts-implementation.md). Binding memeriksa ulang observation dan structural closure, mempertahankan legal date/status sebagai unknown, serta menandai source tanpa structured text sebagai partial. Boundary artifact worker memvalidasi semantic closure dokumen dan `ExtractionBatch`, struktur acyclic, accounting per chunk, bukti versioned, dan containment span dengan indeks serta work cap sebelum registrasi. Workflow durable telah tersambung sampai EXTRACT; helper domain resolution/assembly/retrieval/answer lain masih mengikuti paket pemiliknya. Build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answers.go](answers.go) | Construct claim/citation/stream state helpers over authoritative wire types; preserve semantic vs transport completion. | Test byte-accurate claim spans, unknown evidence refs and exactly one terminal stream event. |
| [chunks.go](chunks.go) | Construct chunk and parent views retaining provision version, source spans and tokenizer identity. | Test missing parents, split Unicode and overlong units without source loss; avoid redundant conversion/allocation across batches. |
| [documents.go](documents.go) | Pertahankan planner/materializer registry-bound dan hubungkan melalui workflow durable tanpa melemahkan provenance, structural coverage, atau uncertainty temporal. | Uji persistence/replay, batch parsial, cross-language semantic closure, workload besar, p95/p99, dan peak RSS. |
| [entities.go](entities.go) | Expose scoped canonical identity/revision and resolution decision helpers without autonomous registry writes. | Test alias ambiguity, merge/split lineage and deterministic identity comparison; avoid redundant conversion/allocation across batches. |
| [registry.go](registry.go) | Pertahankan exact-key planning dan handoff issuer→regulation fail-closed; berikutnya bangun keputusan merge/split reversible di atas registry revision. | Uji false merge/split, stale assignment, duplicate/foreign provenance, concurrent replay, serta throughput batch; semantic merge tetap membutuhkan gold set. |
| [evidence.go](evidence.go) | Expose snapshot-bound evidence/path operations while retaining primary provenance. | Test corpus/version/snapshot mismatches and missing support hydration; avoid redundant conversion/allocation across batches. |
| [relations.go](relations.go) | Expose assertion/support and qualifier invariants without collapsing shared evidence. | Test endpoint types, source withdrawal and negation/condition preservation; avoid redundant conversion/allocation across batches. |
| [operations.go](operations.go) | Pertahankan tipe domain job/publication tanpa dependency SDK storage. | Compile-time interface checks dan integration test adapter/workflow saat field berkembang. |

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [wire.go](wire.go), [wire_test.go](wire_test.go), [boundaries.go](boundaries.go), [boundaries_test.go](boundaries_test.go). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../../../doc/contracts-implementation.md).

`VerifyCitationEvidence` kini menolak sitasi tanpa locator, span kosong, dan span pada evidence multisumber karena daftar `SourceSpans` belum mengikat artefak teks ke source blob. Sitasi multisumber dengan page locator yang terikat blob tetap dapat diperiksa. Pembatasan ini harus diperbarui bersama kontrak pemetaan artefak–sumber saat X01/A01 mengaktifkan bukti multisumber.
