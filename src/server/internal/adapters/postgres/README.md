# src/server/internal/adapters/postgres

`model_completion.go` menyimpan completion EXTRACT dengan first-committed-wins,
payload/usage checksum terikat replay key dan read berbatas byte. Migration 0024
membuat tabel append-only. Tidak ada lock/transaksi selama model berjalan;
concurrent miss dapat sampling dua kali, tetapi caller memakai completion pemenang
yang sama. Row ini bukan checkpoint job/registry/publication; lihat
[kontrak](../../../../../doc/semantic-completion-replay.md).

Runner `migrate.go` dipanggil eksplisit oleh CLI `migrate`; checksum replay dan
transaksi per file tetap sama. Cleanup advisory lock dibatasi lima detik memakai
context baru; kegagalan unlock menutup koneksi agar session lock tidak kembali
ke pool. Deadline operasi utama tetap berasal dari caller.

`graph_resolution_reaffirmation.go` berbagi historical ledger/candidate gate antara
source receipt dan job admission. Policy reaffirmation pada writer source memeriksa
BIND identities serta membandingkan registry stamp di bawah lock; replay mempertahankan
view publication yang sudah dibekukan. [Kontrak](../../../../../doc/graph-reaffirmation.md).

`graph_source_selection.go` membaca seluruh anggota inventory indeks terpublikasi
melalui source receipt terdaftar di bawah lease. Query hanya memilih anggota exact,
membatasi byte sebelum transfer, dan menolak missing/corrupt receipt. Ini discovery,
bukan izin scheduling/publication; [kontrak operator](../../../../../doc/graph-preparation.md).

Query linker memakai `LookupPinnedCanonicalAliases` melalui DTO domain; nama
`RegistryLookupResult` adapter tetap alias kompatibel. Policy dan discovery milik
query/workflow, bukan adapter; lihat [kontrak](../../../../../doc/query-entity-linking.md).

`graph_read.go` mengikat published manifest, scope asal indeks, retained registry,
Neo4j receipt dan applied intent di bawah deadline pin. Snapshot index-only dan
scope/lease salah ditolak; lihat [reader graph](../../../../../doc/graph-read.md).

`index_reuse.go` menyimpan mapping/receipt atomik dan memeriksa origin serta jobs
saat final CAS. `index_read_page.go` menyediakan keyset 64 record/512 KiB dengan
deadline pin; reader target memisahkan source snapshot dari visibility snapshot.
Migration 0023 wajib tersedia; lihat [reuse indeks](../../../../../doc/index-reuse.md).

`graph_readiness.go` menyimpan receipt, applied intent dan authority atomik;
`graph_publication.go` memeriksa stamp registry/source/child di transaksi CAS.
Migration 0022 diperlukan; catalog bukan pengganti guard ini. Lihat
[kontrak](../../../../../doc/graph-readiness.md).

`graph_catalog.go` mereservasi route/write set immutable dan operation planned dalam
transaksi source-admitted yang sama melalui `withCompletedGraph`. Migration 0021
wajib tersedia; pembacaan catalog historis bukan serving admission. Lihat
[kontrak](../../../../../doc/graph-generation-catalog.md).

`graph_job_results.go` mengumpulkan seluruh output STAGED dari admission ASSEMBLE
di bawah lock publication/registry/source/child/pin. Registry drift, cancellation,
checkpoint mismatch dan coverage parsial ditolak; pool satu koneksi didukung.
Hasil masih memerlukan validasi bytes/projection dan bukan receipt publication.
Lihat [persiapan graph](../../../../../doc/graph-publication-preparation.md).

`graph_job_locator.go` memetakan claim ASSEMBLE hidup ke publication untuk restore.
`ClaimGraphJobInScope` memilih scope dari immutable base source snapshot sebelum
claim, sehingga daemon tidak mengambil inventory scope lain. Admission dispatch dan
commit tetap memverifikasi seluruh authority; locator bukan izin publication.

`graph_job_output.go` menyimpan metadata output ASSEMBLE, dependencies immutable,
checkpoint dan STAGED dalam transaksi yang sama. Publication/corpus/source/child/pin
terkunci sebelum recheck authority; helper checkpoint/artifact ikut transaksi tanpa
nested pool acquisition. Port internal mengandalkan byte/projection admission dari
workflow. `GraphCheckpointCommitted` hanya merekonsiliasi checkpoint exact, bukan
mengizinkan pekerjaan baru atau publication Neo4j.

`graph_job_verification.go` menghasilkan admission opaque setelah autentikasi seluruh
source/receipt/view; `graph_jobs.go` memeriksa stamp registry dan authority di bawah
lock sebelum menyimpan inventory serta child jobs atomik. `graph_job_claim.go`
memisahkan claim ASSEMBLE dari generic worker. Migration 0020 wajib diterapkan sebelum
binary claim baru. Namespace alias `lookup:` hanya ditulis writer versioned, termasuk
scope yang sebelumnya kosong. [Kontrak inventory](../../../../../doc/graph-job-inventory.md)
menjelaskan replay, pool satu koneksi dan batas daemon/publication yang masih terbuka.

`graph_job_dispatch.go` memeriksa digest inventory, current stamp, child/source claim,
publication/base dan reader pin melalui satu statement sebelum RPC. Assignment yang
dikembalikan merupakan salinan; commit output wajib mengulang authority setelah RPC.

`document_registry_dependencies.go` mengautentikasi DocumentBatch terdaftar lalu
memeriksa exact key/type dan lifetime BIND pada retained registry revision dalam
transaksi read-only RepeatableRead. Penambahan tak terkait tidak memaksa replan;
identitas berubah/hilang/ditutup ditolak. Read ini bukan authority admission atomik.

`RegistryEntityExport` menjadi alias DTO domain untuk port workflow; response ekspor
tetap RegistryEntityView C01. `graph_receipt_integration_test.go` menyuntikkan drift
intent/decision di atas fixture RESOLVE PostgreSQL nyata untuk memverifikasi reader
graph menolak keputusan yang tidak cocok dengan ledger.

`graph_source_bindings.go` menyimpan receipt transform EXTRACT/RESOLVE immutable
melalui migration 0019. Writer menghitung ulang transform dan memeriksa source
checkpoint, membership, pin serta target fence di bawah lock snapshot/corpus/source.
Reader audit tetap bekerja sesudah abort; receipt tidak membuktikan registry freshness.
Replay dan late cancellation diuji dengan backend nyata; lihat
[verifikasi receipt](../../../../../doc/verification-report-graph-source-receipt.md).

`graph_source_membership.go` memeriksa receipt CHUNK original/bound terhadap inventory
generation yang sudah published dan pin pembaca hidup. Reader berbagi decoder inventory
dalam transaksi yang sama dengan batas payload SQL. Ini membuktikan sumber pada base
snapshot, bukan izin output graph atau kelayakan keputusan registry pada revision baru.

Revalidasi konteks kandidat historis tersedia melalui
`registry_candidate_revalidation.go`: ref/bytes terdaftar, lookup positif/negatif,
alias bersumber dan seluruh profil kandidat dibandingkan pada revision tujuan tanpa
mengubah artefak lama. Ini mendukung persiapan ASSEMBLE; receipt keputusan, dependency
EXTRACT/BIND dan authority publication tetap diperiksa terpisah. Lihat
[kontrak coordinator](../../../../../doc/graph-assembly-coordinator.md).

[semantic_proposal_queue.go](semantic_proposal_queue.go) menyimpan locator artefak advisory RESOLVE dan transisi `WAITING_REVIEW` atomik, dengan fence/checkpoint/corpus, cancellation priority, serta lease release. Migration 0012 wajib tersedia. Queue tidak memberi approval LINK; [semantic_proposal_queue_integration_test.go](semantic_proposal_queue_integration_test.go) memeriksa park/cancel, stale fence, foreign corpus, metadata drift, dan rollback nyata. Reader mengembalikan locator response per corpus/job; workflow review berikutnya wajib memvalidasi ulang bytes/dependency sebelum keputusan.

Adapter penyimpanan metadata dokumen, versi, manifest ingestion, dan status workflow pada PostgreSQL. Adapter Go memakai koneksi yang dipakai ulang. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menyimpan kebijakan ranking atau menggantikan Neo4j sebagai implementasi traversal. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menangani transaksi lokal, keunikan ID, pagination, dan pool koneksi. Transaksi PostgreSQL tidak dianggap mencakup Qdrant atau Neo4j; perubahan schema mengikuti migrations.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[registry_semantic_receipt.go](registry_semantic_receipt.go) membaca ulang receipt
committed tanpa lease RESOLVE atau mutasi registry. Review historis diperiksa read-only;
writer tetap memakai row lock. [graph_assembly_authority.go](graph_assembly_authority.go)
memeriksa checkpoint RESOLVE STAGED dan publication/base/registry binding sebelum
persiapan graph. Pemeriksaan ini belum transaksi scheduling/commit; lihat
[kontrak coordinator](../../../../../doc/graph-assembly-coordinator.md).

[registry_entity_view.go](registry_entity_view.go) mengekspor exact canonical selection
pada revision publication-bound dalam satu transaksi read-only. Hash profil, kolom,
identity dan budget diperiksa; item hilang menggagalkan ekspor. Pembaca ini tidak
menyimpan artefak atau membuktikan live fence setelah transaksi; coordinator melakukan
admission/commit ulang. Lihat [kontrak input ASSEMBLE](../../../../../doc/graph-assembly-inputs.md).

[registry_snapshot.go](registry_snapshot.go) mengikat publication ke revision registry
immutable di bawah fence. Reader pinned di [registry_candidates.go](registry_candidates.go)
memeriksa live lease dan history positif/negatif migration 0018. Writer alias menyimpan
count history atomik; scope alias tidak dapat diubah lewat writer lookup generik.
Lihat [kontrak dan batas migrasi](../../../../../doc/registry-history.md).
Ini primitive library; wiring graph dan entity linker request masih diperlukan.

[semantic_review.go](semantic_review.go) menyimpan LINK reviews dalam batch, intent,
audit dan resume dalam satu transaksi corpus/job. Migration 0017 wajib tersedia.
Caller workflow mengautentikasi operator dan bytes; adapter mengunci state/revision,
checkpoint dan cancellation. Exact replay tidak mengubah status atau budget job.

`artifacts.go` menyediakan `EnsureArtifactDependencyManifest` untuk import
statistik: artifact row dikunci dan replay harus memakai seluruh dependency serta
producer hash identik. Metode replacement lama tetap tersedia bagi pipeline yang
memerlukannya; tidak ada klaim dependency immutable pada seluruh API database.

[index_jobs.go](index_jobs.go) menyimpan inventory dan child job INDEX atomik;
[index_job_claim.go](index_job_claim.go) mengklaim child dengan fence/retry dan
publication aktif. Migration 0015 wajib tersedia. Generic claim melewati child
berinventory; caller wajib memeriksa authority lagi sebelum commit output.
Lihat [kontrak inventory](../../../../../doc/index-job-inventory.md).

[index_job_output.go](index_job_output.go) meng-commit checkpoint output yang
telah diverifikasi caller dan status STAGED dalam transaksi yang sama. Plan,
source checkpoint/cancellation, publisher fence, artifact registration dan lease
setelah lock wajib cocok. Fungsi ini tidak membaca file atau mempublikasikan indeks.
`IndexCheckpointCommitted` mengonfirmasi exact payload hash dan STAGED setelah
acknowledgement commit hilang; `IndexJobPublication` memeriksa lease/source/fence
sebelum dispatch, termasuk ketika inventory admitted masih berada di cache.

[index_catalog.go](index_catalog.go) menyimpan generation/route dan record point
immutable dengan collision check UUID di bawah fence publication. Migration 0014
wajib tersedia; record terikat byte payload dan replay tidak boleh mengubahnya.
[index_writer.go](index_writer.go) menyediakan session lock, admission checkpoint
CHUNK/dictionary dan planned intent sebelum backend I/O. Ini primitive internal;
autentikasi byte serta kelengkapan sumber dilakukan preparation
[writer awal](../../../../../doc/initial-index-writer.md). Caller menyisakan
koneksi pool untuk transaksi selain koneksi session lock. Commit publication
memeriksa publisher fence sebelum aktivasi; replay snapshot yang sudah published
tidak mengaktifkan ulang pointer lama. Tes DB nyata mencakup collision, rollback,
immutability, lock/retry serta injected epoch drift. Required benchmark belum diukur.

[index_dictionary.go](index_dictionary.go) mengalokasikan ID term BM25 `uint32` secara
append-only per corpus/analyzer, mengembalikan revision dan replay operation key,
serta membaca dictionary pada revisi terpin dengan batas item. Migration 0013
wajib diterapkan sebelum pemanggil INDEX memakai adapter ini. SQL menyimpan
mapping dan ledger; exporter typed tersedia sebagai library, sedangkan bukti
lineage otoritatif yang dipakai publication masih pekerjaan X01. Jalur query tidak boleh
memanggil allocator per token. Ukur p95/p99, pool wait, contention, ukuran
dictionary, dan RSS pada corpus referensi; target tetap REQUIRED_UNMEASURED.

[domain/index_dictionary_fingerprint.go](../../domain/index_dictionary_fingerprint.go) memberi nama
revisi `lexrev:<angka>` dan fingerprint mapping yang sama dengan reader Rust.
Hash ini tidak memasukkan corpus; artifact owner dan pemanggil publication wajib
mengikat corpus serta revision PostgreSQL terverifikasi secara terpisah. Fixture
lintas bahasa sudah lulus. [index_dictionary_artifact.go](index_dictionary_artifact.go)
mengekspor snapshot penuh pada revisi terpin ke `LexicalDictionaryArtifact`, dengan
batas jumlah term dan wire bytes; kelebihan batas menghasilkan error, bukan truncation.
Ekspor ini root-only: tidak menyatakan ancestry historis dan tidak menulis storage.
Pemanggil masih harus menyimpan bytes secara immutable, mengikat manifest generation
dan membuktikan registry authority sebelum publikasi. Pembacaan SQL dibatasi jumlah
term; batas wire bytes diperiksa setelah mapping dimuat, bukan batas peak RSS.
Budget item wire adalah `2 * jumlah_term + 2` untuk root (tambah satu untuk
parent hash pada child). Exporter memeriksa kapasitas ini sebelum SQL; default
100000 wire items memuat maksimal 49999 term root, dan vocabulary lebih besar
memerlukan konfigurasi budget eksplisit, bukan truncation atau kenaikan otomatis.

[repository.go](repository.go) mengelola lifecycle pool dan error boundary. [migrate.go](migrate.go) menerapkan migration terurut dengan advisory lock serta checksum. [jobs.go](jobs.go) mengelola idempotency, claim PARSE/STRUCTURE/BIND/CHUNK/EXTRACT/RESOLVE, attempt global dan budget retry per stage, lease/fence, polling cancellation, checkpoint, dan completion atomik yang memberi prioritas pada cancellation. Klaim RESOLVE mensyaratkan checkpoint EXTRACT dengan terminal sukses dan tidak diambil claimant generik. [artifacts.go](artifacts.go) mengikat serta memuat metadata immutable untuk handoff checkpoint. [registry.go](registry.go) mengalokasikan exact canonical identity secara revisioned dan idempotent. [registry_aliases.go](registry_aliases.go) meregistrasikan profil/alias bersumber secara append-only dengan CAS revision, sedangkan [registry_candidates.go](registry_candidates.go) membaca kandidat ambigu dan revision lookup positif/negatif dalam satu snapshot. [publication.go](publication.go) merealisasikan reservation, backend receipt, snapshot CAS, outbox, abort, dan read lease. [repository_integration_test.go](repository_integration_test.go) dan [registry_aliases_integration_test.go](registry_aliases_integration_test.go) adalah suite PostgreSQL aktual dan akan skip jika DSN test tidak tersedia.

[registry_candidate_batch.go](registry_candidate_batch.go) membaca scope yang dipilih eksplisit per mention
dalam satu transaksi lookup, memeriksa key/revisi serta closure alias, kemudian meneruskan observasi ke
builder artefak C01. Nol mention menghasilkan `ErrNoCandidateMentions` tanpa pembacaan registry agar workflow
dapat melewati RESOLVE secara eksplisit. [registry_candidate_batch_test.go](registry_candidate_batch_test.go)
memeriksa mapping dan hasil tidak mungkin dengan fixture. Workflow kandidat sekarang menguji pembacaan PostgreSQL nyata, fencing EXTRACT, dan penyimpanan artefak berhash dalam tes integrasi semantik.
`registry_aliases.go` menerima seluruh tipe ontology v1 melalui kode identitas stabil di domain; tes PostgreSQL menegaskan alias `provision` pada satu regulation scope tidak bocor ke peraturan lain. Tes tersebut memeriksa storage/scope, belum membuktikan rantai BIND ke alias sourced atau kualitas resolusi hukum.

[registry_semantic.go](registry_semantic.go) menerima proposal LINK/DEFER terikat batch kandidat dan melakukan
CAS revision dalam transaksi serializable. [registry_semantic_inputs.go](registry_semantic_inputs.go) memeriksa
hash/metadata artefak terdaftar serta baris review LINK yang mengikat proposal/kandidat persis secara batch;
[registry_semantic_fence.go](registry_semantic_fence.go) mengunci job/checkpoint dan memeriksa corpus,
owner, fence, cancellation, lease, manifest EXTRACT, dan hash sumber di transaksi yang sama dengan
CAS. [registry_semantic_replay.go](registry_semantic_replay.go) merekonstruksi receipt lama dengan pemeriksaan
integritas row/payload. [registry_semantic_integration_test.go](registry_semantic_integration_test.go)
menguji writer ini pada PostgreSQL disposable, termasuk retry, stale candidate, forgery, konkurensi,
handoff FileStore nyata, dan lease yang berubah di tengah pembacaan/transaksi. Caller wajib memakai
`SemanticResolutionHandoff` agar byte berasal dari `ReadVerified`. [registry_semantic_intent.go](registry_semantic_intent.go) menyimpan intent append-only sebelum CAS agar retry membawa request/review yang sama; [registry_semantic_empty.go](registry_semantic_empty.go) membaca revisi terikat lease untuk EXTRACT kosong maupun validasi kandidat di sekitar lookup. Writer membedakan kandidat/review permanen stale yang menuntut job baru dari review yang belum tersimpan dan masih dapat tiba. Producer review terautentikasi belum tersedia dan adapter ini belum menjadi stage RESOLVE publik.

## Benchmark dan perhatian performa

**STORAGE.** Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Producer kandidat untuk scope eksplisit, writer receipt LINK/DEFER, handoff EXTRACT terfence, intent sebelum CAS, dan checkpoint/recovery RESOLVE
sudah diuji pada PostgreSQL aktual, tetapi producer/dispatch RESOLVE lengkap belum aktif. Producer review
terautentikasi dan alias `support_refs` tetap memerlukan integrasi/provenance sebelum publikasi;
writer tidak mengesahkan kebenaran identitas semantik dari gold hukum. Keputusan ditulis dengan
batch SQL untuk menekan perjalanan database di bawah lock corpus; latency dan throughput target masih
REQUIRED_UNMEASURED dan harus diukur bersama antrean/pool pada workload referensi. Verifikasi byte
sekarang membaca dan meng-hash artefak berulang, sementara lock job menahan lease renewal sepanjang
transaksi; profilkan I/O, lock wait, peak RSS, dan lama lease sebelum memberi klaim performa.

Fondasi S01 untuk schema migration, artifact metadata/dependency, durable jobs, retry availability dan budget per stage, handoff PARSE→STRUCTURE→BIND→CHUNK→EXTRACT, publication ledger, active-snapshot pointer, dan read lease telah aktif. Exact allocator K01 mengunci revision corpus, menyimpan operation ledger, dan mendeteksi replay/corruption; serialization failure PostgreSQL `40001` dikembalikan agar caller mengulang dengan budget. Registry alias K01 menyimpan alias unreviewed pada canonical ID yang sudah ada, mengembalikan semua kandidat ambigu dalam batas caller, dan mencatat perubahan lookup kosong; replay/reuse dan read memeriksa payload/hash/kolom persisten. Executor BIND memakai adapter ini untuk registry batch idempotent, artefak immutable, fingerprint dependency, empty lookup, dan checkpoint fenced. Belum ada producer alias produksi atau callsite RESOLVE; `support_refs` baru divalidasi bentuknya dan harus dibuktikan merujuk artefak EXTRACT sebelum publikasi. Canonical resolution semantik/merge-split, dependency closure U01, backend mutation, retention/GC, dan benchmark performa masih mengikuti paket pemiliknya.

Checkpoint PARSE, STRUCTURE, BIND, CHUNK, dan EXTRACT menyimpan terminal outcome di payload dan kolom terpisah. Setelah lease kedaluwarsa, coordinator dapat merekonsiliasi output sukses, parsial, atau dibatalkan tanpa menambah budget stage, termasuk pada attempt maksimum. Ketidaksesuaian payload/kolom ditolak sebagai integrity error; checkpoint lama dengan outcome `NULL` tidak recovery-eligible dan tidak dianggap sukses.

Rollout harus menjaga migration 0004, worker Rust, dan coordinator Go dalam satu compatibility window: migration diterapkan sebelum producer baru menulis outcome, coordinator baru menerima row legacy `NULL` secara fail-safe, dan worker lama tidak boleh dipasangkan dengan guard response baru sebagai jalur produksi. Rollback aplikasi tetap mempertahankan kolom nullable; migration yang sudah tercatat tidak diedit atau diturunkan secara in-place.

## Rekomendasi implementasi anak

[extraction_evidence.go](extraction_evidence.go) menyimpan locator mention EXTRACT bersama checkpoint sukses secara atomik melalui migration 0011. Lookup support alias memakai satu query berbatas pada corpus/snapshot/auth yang sama; missing support dan hasil berlebih menghasilkan error eksplisit. Katalog append-only tidak menggantikan verifikasi hash/closure di workflow dan tidak membackfill artefak lama secara otomatis. Integration test memeriksa replay, rollback, cancellation/fence, isolasi scope, serta limit pada PostgreSQL disposable. Ukur lookup/lock p95/p99 dan pertumbuhan indeks sebelum acceptance.

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [repository.go](repository.go) dan [migrate.go](migrate.go) | Pertahankan pool eksplisit dan migration checksum; tambahkan rollout migration hanya melalui file bernomor baru. | Uji schema kosong, replay, checksum drift, timeout, serta upgrade snapshot produksi. |
| [jobs.go](jobs.go) | Pertahankan claim PARSE/STRUCTURE/BIND/CHUNK/EXTRACT, cancellation fenced, dan retry per-stage; tambah lease heartbeat serta API cancellation. | Failure injection pada expiry/renewal/checkpoint, verifikasi stage ownership dan attempt monotonic, lock contention, serta ukur queue time dan pool saturation. |
| [registry.go](registry.go) | Pertahankan atomic exact-key allocation, operation ledger, historical replay, dan revision CAS; tambah merge/split/review hanya melalui keputusan revisioned. | Uji serialization retry, concurrent same-key allocation, stale revision, partial restore/corruption, legacy upgrade, serta p50/p95/p99 batch. |
| [registry_aliases.go](registry_aliases.go) dan [registry_candidates.go](registry_candidates.go) | Hubungkan producer bersumber dan stage RESOLVE setelah validasi keberadaan `support_refs`; tambahkan review, closure, dan query historis melalui keputusan revisioned. | Uji candidate coverage dan false merge/split pada gold, replay/corruption, concurrent write, ukuran indeks, pool wait, serta p50/p95/p99 lookup/batch. |
| [artifacts.go](artifacts.go) | Gunakan dependency rows untuk closure U01 dan batch registration. | Bandingkan closure incremental dengan rebuild dan ukur reverse lookup pada corpus referensi. |
| [publication.go](publication.go) | Sambungkan backend operations, compensation, retention, dan recovery U01/O01. | Injeksi crash di setiap langkah, verifikasi historical visibility, read lease, dan pool saturation. |

[index_read.go](index_read.go) membaca generation PUBLISHED dan receipt di bawah
lease snapshot hidup, lalu mengambil record secara batch dengan batas byte SQL.
Owner/corpus/sequence/expiry, digest point, payload dan visibility diperiksa;
missing record tidak berubah menjadi partial success. Snapshot tidak diganti
ke pointer aktif lain selama request. Lihat [kontrak baca](../../../../../doc/pinned-evidence.md)
dan [verifikasi](../../../../../doc/verification-report-pinned-evidence.md).

Allocator lexical mengulang transaksi yang dipastikan rollback (SQLSTATE 40001
atau 40P01) maksimal empat attempts dengan penantian berbatas context. Konflik
semantik atau commit outcome tidak diketahui tetap error. Regression test
memeriksa klasifikasi, batas retry dan cancellation; latency antre/retry tetap
harus masuk pengukuran workload.

`index_source_bindings.go` menyimpan receipt immutable original/bound (migration0016 wajib untuk runtime baru). `index_job_results.go` membaca output lengkap dengan checkpoint/state/fence/hash; `index_publication.go` mengunci seluruh child/source sebelum activation untuk menolak late cancellation. NOWAIT menghasilkan not-ready saat job sedang berubah, sehingga tidak menunggu lock terbalik. Lihat [kontrak](../../../../../doc/index-source-publication.md).
