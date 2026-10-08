# src/contracts/proto/regulagraph/v1

Lokasi kontrak wire versi awal untuk dokumen, job, graph delta, bukti, jawaban, serta inference. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

File sudah mendefinisikan message, enum, aturan field dan service descriptor C01. Katalog field semantik seluruh sistem telah dirancang pada [system-contracts](../../../../../doc/system-contracts.md), termasuk ownership, API/RPC/event, ID, presence, error, dan offset UTF-8 byte end-exclusive. Cakupan codegen, validator dan fixture yang tersedia dijelaskan pada [implementasi C01](../../../../../doc/contracts-implementation.md); konsumen produksi tetap memerlukan verifikasi integrasinya.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

`RegistryEntityView`, `GraphAssemblyPlan` dan `ProcessBatchRequest.graph_assembly_plan`
menambahkan seleksi registry revision-bound dan role input ASSEMBLE secara aditif.
Baseline tetap; wire transport mempertahankan unknown fields, sementara gate stage
menolaknya. Library exporter/validator dan handler worker Rust tersedia; coordinator graph belum tersambung.
Lihat [kontrak input ASSEMBLE](../../../../../doc/graph-assembly-inputs.md).

`IndexBuildPlan`, `ProcessBatchRequest.index_build_plan`, `IndexBatch.build_plan`
dan `LexicalStatisticsArtifact.input_policy` menambahkan handoff INDEX terpin secara
aditif. Konsumen stage INDEX menolak plan/policy hilang; pembaca wire lama tetap
mempertahankan unknown fields. Baseline schema tetap; lihat
[kontrak INDEX](../../../../../doc/index-build.md).

`evidence.proto` juga memuat artefak analyzer/statistik BM25 typed yang digunakan
builder Rust dan reader Go. Penambahan pesan bersifat aditif; baseline schema
tidak ditulis ulang. Semantik formula, empty chunks dan fingerprint populasi
dijelaskan pada [kontrak lexical](../../../../../doc/lexical-generation.md).

Berkas: [answers.proto](answers.proto), [common.proto](common.proto), [documents.proto](documents.proto), [evidence.proto](evidence.proto), [graph.proto](graph.proto), [inference.proto](inference.proto), [jobs.proto](jobs.proto).

`RegistryCandidateBatch` adalah kontrak handoff read-only antara EXTRACT dan RESOLVE: hasil kandidat serta lookup kosong dicatat per scope bersama revisi, tipe, canonical scope, dan key normalisasi. Kontrak aditif masuk schema lock setelah review independen. Pembaca registry dan konsumen RESOLVE produksi belum tersedia; validasi struktur tidak membuktikan kebenaran hasil query database.

`FilterMetadata.provision_filters` menambahkan `IndexProvisionFilter` secara aditif
untuk mengikat ID versi pasal, regulasi, blob sumber, interval, status hukum, dan
yurisdiksi pada satu record. Array filter lama tetap dapat dibaca demi kompatibilitas,
tetapi tidak membuktikan pasangan versi/status; writer X01 baru harus memakai record
berpasangan. `IndexGeneration.filter_format` mem-pin bentuk itu pada generation baru;
`embedding_input_policy` (tag 8 aditif) mem-pin policy render teks dense. X01
baru menerima `structure-labels-v1` dan menolak field kosong/tidak dikenal pada
admission reader/writer. Field opsional secara wire menjaga decode generasi lama;
validasi stage-specific menentukan apakah generation boleh dilayani.
Publisher harus menahan generation baru sampai semua pembaca yang dapat dirutekan
memahami `PAIRED_PROVISION_V1` dan `embedding_input_policy`; parser binary lama
dapat mengabaikan tag 8 dan tetap melayani hasil yang salah. Upgrade seluruh
route atau isolasi route lama harus dibuktikan sebelum publication. Writer dan
reader backend belum aktif sebagai pipeline publikasi.

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. `IngestionRequest` dan `ProcessBatchRequest` membawa `SourceObservation` agar provenance metadata portal tetap terikat pada hash PDF sampai `DocumentBatch`. Kontrak checkpoint mengikat terminal outcome worker agar recovery PARSE/STRUCTURE/CHUNK/EXTRACT mempertahankan hasil sukses, parsial, dan pembatalan; nilai `UNSPECIFIED` tetap dipertahankan untuk membaca checkpoint lama secara fail-safe. `JobStage` menambahkan BIND dan CHUNK secara append-only; urutan pipeline tidak boleh disimpulkan dari angka enum dan memakai rank domain eksplisit. `ExtractionBatch` dan `ResolutionBatch` memisahkan proposal graph sebelum dan sesudah canonical resolution dari `GraphDelta` yang baru dibentuk pada ASSEMBLE. `Qualifier.mention_id` membawa referensi provisional selama EXTRACT; hanya RESOLVE/ASSEMBLE yang boleh mengubahnya menjadi `canonical_id`. Executor graph/retrieval, mutasi backend, provider/model produksi, gold dataset, dan acceptance produksi belum aktif.

Penambahan resolusi kontekstual memakai tag aditif: `AmbiguousMention.context_items=6`, `ResolutionProposal.rationale=11`, dan `supporting_context_ids=12`. Pembaca binary lama tetap dapat membaca field lama; gateway RESOLVE baru menolak request tanpa konteks secara eksplisit. Schema lock lama dipertahankan dan compatibility check dijalankan; fixture baru menguji round trip keempat bahasa. Lihat [kontrak runtime resolusi](../../../../../doc/semantic-resolution.md).

`AmbiguousMention.candidate_contexts=7` menambah `ResolutionCandidateContext` berisi canonical ID, alias ID, supporting Mention, dan excerpt sumber. Field lama/baseline tidak diubah. Gateway dan workflow baru mensyaratkan citation mention serta kandidat terpilih untuk LINK; pembaca lama yang mengabaikan field tidak memberikan jaminan perilaku tersebut, sehingga runtime/prompt perlu diperbarui bersama. Fixture lintas bahasa menguji preservation serta penolakan support/context wajib yang hilang.

`LexicalDictionaryArtifact` dan `LexicalDictionaryEntry` menambahkan kontrak
snapshot penuh secara aditif. `IndexGeneration.lexical_dictionary` menunjuk bytes
artefak yang hash-nya diverifikasi storage. Mapping fingerprint v1 mengikat analyzer,
`lexrev:<registry_revision>`, dan pasangan term-ID; corpus dan metadata terikat
oleh validasi stage serta hash bytes artefak, bukan hash mapping. Entries harus
urut byte UTF-8 secara ketat. Parent revision/hash wajib sama-sama hadir atau
absen dan hanya memberi ancestry bila parent aktual sudah diperiksa. Reader
baru diperlukan sebelum writer memakai jenis artefak ini; decode schema saja
tidak membuktikan registry authority, ancestry, atau readiness publication.

Pada `LexicalDictionaryArtifact`, `registry_revision` dan `parent_registry_revision`
adalah revisi `lexical_dictionary_state` per corpus/analyzer. Keduanya berbeda
dari revisi canonical entity registry K01; jangan mengikat dictionary ke
`RegistryRevision` graph atau menjadikannya bukti perubahan identitas entitas.
