# src/contracts/proto/regulagraph/v1

Lokasi kontrak wire versi awal untuk dokumen, job, graph delta, bukti, jawaban, serta inference. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Fungsi di luar cakupan ini mengikuti komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

File sudah mendefinisikan message, enum, aturan field dan service descriptor C01. Katalog field semantik seluruh sistem telah dirancang pada [system-contracts](../../../../../doc/system-contracts.md), termasuk ownership, API/RPC/event, ID, presence, error, dan offset UTF-8 byte end-exclusive. Cakupan codegen, validator dan fixture yang tersedia dijelaskan pada [implementasi C01](../../../../../doc/contracts-implementation.md); konsumen produksi tetap memerlukan verifikasi integrasinya.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [answers.proto](answers.proto), [common.proto](common.proto), [documents.proto](documents.proto), [evidence.proto](evidence.proto), [graph.proto](graph.proto), [inference.proto](inference.proto), [jobs.proto](jobs.proto).

`RegistryCandidateBatch` adalah kontrak handoff read-only antara EXTRACT dan RESOLVE: hasil kandidat serta lookup kosong dicatat per scope bersama revisi, tipe, canonical scope, dan key normalisasi. Kontrak aditif masuk schema lock setelah review independen. Pembaca registry dan konsumen RESOLVE produksi belum tersedia; validasi struktur tidak membuktikan kebenaran hasil query database.

`FilterMetadata.provision_filters` menambahkan `IndexProvisionFilter` secara aditif
untuk mengikat ID versi pasal, regulasi, blob sumber, interval, status hukum, dan
yurisdiksi pada satu record. Array filter lama tetap dapat dibaca demi kompatibilitas,
tetapi tidak membuktikan pasangan versi/status; writer X01 baru harus memakai record
berpasangan. `IndexGeneration.filter_format` mem-pin bentuk itu pada generation baru.
Publisher harus menahan generation baru sampai semua pembaca yang dapat dirutekan
memahami `PAIRED_PROVISION_V1`; parser binary lama mengabaikan field baru sehingga
kompatibilitas decode saja tidak cukup. Writer dan reader backend belum aktif.

## Benchmark dan perhatian performa

Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml) (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. `IngestionRequest` dan `ProcessBatchRequest` membawa `SourceObservation` agar provenance metadata portal tetap terikat pada hash PDF sampai `DocumentBatch`. Kontrak checkpoint mengikat terminal outcome worker agar recovery PARSE/STRUCTURE/CHUNK/EXTRACT mempertahankan hasil sukses, parsial, dan pembatalan; nilai `UNSPECIFIED` tetap dipertahankan untuk membaca checkpoint lama secara fail-safe. `JobStage` menambahkan BIND dan CHUNK secara append-only; urutan pipeline tidak boleh disimpulkan dari angka enum dan memakai rank domain eksplisit. `ExtractionBatch` dan `ResolutionBatch` memisahkan proposal graph sebelum dan sesudah canonical resolution dari `GraphDelta` yang baru dibentuk pada ASSEMBLE. `Qualifier.mention_id` membawa referensi provisional selama EXTRACT; hanya RESOLVE/ASSEMBLE yang boleh mengubahnya menjadi `canonical_id`. Executor graph/retrieval, mutasi backend, provider/model produksi, gold dataset, dan acceptance produksi belum aktif.

Penambahan resolusi kontekstual memakai tag aditif: `AmbiguousMention.context_items=6`, `ResolutionProposal.rationale=11`, dan `supporting_context_ids=12`. Pembaca binary lama tetap dapat membaca field lama; gateway RESOLVE baru menolak request tanpa konteks secara eksplisit. Schema lock lama dipertahankan dan compatibility check dijalankan; fixture baru menguji round trip keempat bahasa. Lihat [kontrak runtime resolusi](../../../../../doc/semantic-resolution.md).

`AmbiguousMention.candidate_contexts=7` menambah `ResolutionCandidateContext` berisi canonical ID, alias ID, supporting Mention, dan excerpt sumber. Field lama/baseline tidak diubah. Gateway dan workflow baru mensyaratkan citation mention serta kandidat terpilih untuk LINK; pembaca lama yang mengabaikan field tidak memberikan jaminan perilaku tersebut, sehingga runtime/prompt perlu diperbarui bersama. Fixture lintas bahasa menguji preservation serta penolakan support/context wajib yang hilang.
