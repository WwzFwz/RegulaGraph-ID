# Coordinator ASSEMBLE: authority dan handoff

Dokumen ini menjelaskan batas authority Go di depan [worker ASSEMBLE](assembly-worker.md)
dan pekerjaan integrasi berikutnya. Reader/gate PostgreSQL di bawah sudah tersedia;
persiapan plan/view, inventory job, dispatch dan commit output graph belum dirangkai
menjadi workflow lengkap. Semua akses dilakukan caller yang telah mengautentikasi corpus.

## Authority yang tersedia

Library `PrepareGraphAssembly` kini menyimpan plan dan canonical view dari receipt
sumber, dengan rekonstruksi keputusan historis, ontology pin dan budget worker.
[Kontrak persiapan](graph-assembly-inputs.md) menjelaskan batasnya: locator artefak
tidak menggantikan dependency freshness atau authority transaksi admission job.

`ReadCommittedSemanticResolution` menerima bytes EXTRACT/kandidat, request asli dan
approval review. Adapter memeriksa artefak terdaftar serta hash/schema/corpus, proposal
dan candidate cardinality, lalu membaca operation/decision ledger dalam transaksi
read-only RepeatableRead. Request hash, revision, correlation, assignment, payload hash,
review dan receipt deterministik harus cocok. Operasi yang belum pernah di-commit
menghasilkan NotFound; fungsi tidak membuat keputusan, mengklaim job atau menaikkan
revision. Transport request ID dapat berubah sesuai semantik replay existing, tetapi
source/config/auth scope dan keputusan tetap terikat. Revisi registry yang lebih baru
tidak menulis ulang keputusan historis. Jalur ini hanya untuk receipt nonempty;
mention-free RESOLVE menggunakan checkpoint/sumber tanpa operation ledger tersendiri.

`VerifyGraphAssemblySourceCheckpoint` menerima corpus, source job, checkpoint ID dan
ref ResolutionBatch. Artefak harus cocok persis dengan registrasi; job tidak dibatalkan,
berada pada RESOLVE STAGED/SUCCEEDED, dan latest checkpoint harus mengikat output
sukses pada fence job yang sama. Payload hash, ID/corpus/schema, stage, terminal status,
artifact ID/hash dan fence diperiksa. Artefak sah dari job lain atau checkpoint lama
tidak otomatis merupakan sumber job ini.

`VerifyGraphAssemblyPublication` menerima plan yang sudah lolos gate domain. Satu
query memeriksa target STAGING/VALIDATING, sequence/fence, publisher aktif, parent yang
PUBLISHED dan masih aktif, identitas/hash/generation base, serta binding registry
immutable yang masih berada dalam rentang history. Published/aborted target tidak
diterima untuk pekerjaan baru. Method ini tidak memublikasikan snapshot.

Ketiganya adalah pemeriksaan awal. Tidak ada lease atau lock yang bertahan setelah
fungsi kembali. Inventory scheduling dan output commit harus mengulang predicate
yang relevan dalam transaksi dengan lock publication/job/source yang konsisten.
Gate read tidak boleh dianggap sebagai bukti bahwa fence tetap hidup saat RPC selesai.

`VerifyRegistryCandidateView` mengautentikasi ref/bytes EXTRACT dan candidate batch
yang telah terdaftar, lalu membaca seluruh key alias secara batch pada revision tujuan
eksplisit. Scope positif/negatif, revision scope, membership kandidat, seluruh payload
alias/support dan profil canonical harus sama dengan konteks yang dilihat resolver.
Perubahan global yang tidak menyentuh konteks tersebut tidak membatalkan reuse;
perubahan kandidat yang tidak dipilih pun menghasilkan `ErrResolutionReplan`.
Artefak asli tidak diubah. Method ini tidak menghasilkan keputusan atau receipt baru.

Gate kandidat kini dikonsumsi `ReadGraphResolutionForAssembly` setelah receipt
historis direkonstruksi dan sebelum preparation mengekspor view. Reader memakai
bytes kandidat yang sama, membatasi lookup, dan memeriksa dependency source-only
EXTRACT saat ini. Writer LINK/DEFER hanya menaikkan revision dan menulis ledger
keputusan; ia tidak menulis alias. Karena itu gate tidak memerlukan pengecualian
perubahan alias milik operasi sendiri. Perubahan konteks tetap meminta replan.
Revision hasil RESOLVE wajib sama dengan target; belum ada receipt reaffirmation.

Pemeriksaan kandidat ini belum cukup untuk memperluas guard revision GraphDelta.
Caller harus membuktikan receipt committed, dependency EXTRACT/dokumen (termasuk
scope luas `canonical-registry` dari BIND), keanggotaan snapshot sumber, serta binding
revision publication. Receipt pada revision R tidak otomatis dapat dipakai pada R+1
hanya karena candidate view sama. Alias baru dari commit RESOLVE sendiri juga tidak
dikecualikan diam-diam: jika mengubah konteks, perlu replan/reaffirmation eksplisit.

## Envelope sumber dan membership snapshot

`BindGraphSourceEnvelopes` menerima empat artefak berhash: CHUNK asli tanpa snapshot,
CHUNK snapshot-bound, EXTRACT asli, dan RESOLVE asli. Jumlah bytes input dibatasi 16 MiB
sebelum decode, output EXTRACT/RESOLVE bersama dibatasi 16 MiB. Schema/media/unknown
fields, closure EXTRACT/RESOLVE dan hash seluruh input diperiksa. CHUNK bound harus
identik dengan hasil `BindInitialSnapshotSource` untuk job dan snapshot tersebut;
penggantian judul, teks, chunk atau metadata di luar transform itu ditolak.

Hasilnya dua artefak baru content-addressed. ID root, request/trace, snapshot/config
context dan immediate source ref menunjuk envelope baru. Dependency asli, termasuk
observasi lookup negatif, tetap ada; hash artefak asal dan policy binder ditambahkan.
Mention, assertion, support, proposal, assignment canonical, model/prompt/producers,
counts dan recorded registry revision tidak diubah. Producer tetap menyatakan proses
inference asli; dependency policy menyatakan transform metadata sesudahnya. Referensi
diagnostik `ResolutionBatch.Issues.EvidenceRefs` yang menunjuk root EXTRACT/RESOLVE lama
diremap ke root baru. Referensi record bukti lain tidak diubah; diagnostik asli masih
tersimpan pada artefak asal. Collision root dengan record anak ditolak agar remap
tidak ambigu. Helper tidak memanggil LLM, menulis storage, atau mengesahkan receipt.

`VerifyPublishedGraphSourceBinding` membuktikan pasangan CHUNK asli/bound melalui pin
snapshot yang masih hidup. Reader memakai publication/index generation yang sudah
published dan memiliki receipt backend, inventory terverifikasi yang memuat source job
serta bound ref tersebut, snapshot/scope/fence asal yang exact, immutable source-binding
receipt, dan registrasi kedua artefak. Lease diperiksa lagi sebelum transaksi read selesai.
Inventory dibaca dalam transaksi yang sama; payload plan dibatasi SQL sebelum transfer
(16 MiB per plan, 64 MiB seluruh plan, maksimal 256 plan). Binding yang dibuat sebelum
publication atau yang tidak tercantum dalam inventory bukan bukti membership published.

Membership ini merupakan fakta snapshot historis; source cancellation/current RESOLVE
checkpoint dan otoritas target graph tetap diperiksa terpisah. Scope saat ini memakai
receipt initial-index yang tersedia, bukan reuse bebas antar snapshot incremental.
Receipt original-to-bound EXTRACT/RESOLVE dan workflow penyimpanannya kini tersedia
seperti di bawah. Komposisi seluruh gate dan penjadwalan ASSEMBLE masih harus diselesaikan.
Jangan menganggap hasil helper murni atau membership lama sebagai izin publikasi graph.

## Penyimpanan envelope dan receipt graph

`workflows.BindGraphSource` menerima binding target tanpa output, pin base hidup,
registered references CHUNK asli/bound serta EXTRACT/RESOLVE asli, reader dan writer
artefak. Input gabungan dibatasi 16 MiB sebelum I/O. Workflow memeriksa membership
dan checkpoint, membaca byte terverifikasi, lalu menghasilkan dua envelope deterministik.
Byte disimpan immutable, referensi didaftarkan, dan dependency manifest diindeks dengan
owner physical artifact ID; manifest di dalam byte tetap memakai logical batch ID.
Lookup observations dan producer asli tidak diubah oleh pemetaan owner storage tersebut.

`RegisterGraphSourceBinding` menghitung ulang transform dari empat input asli sebelum
mengambil lock. Output references wajib identik. Transaksi mengambil lock target snapshot,
corpus, lalu source job; memeriksa ulang publication/base/registry binding, checkpoint
RESOLVE terbaru, cancellation, published membership dan semua referensi terdaftar.
Live lease diperiksa sebelum commit. Migration 0019 menyimpan satu receipt immutable
per `(publication_id, source_job_id)` dengan payload berhash, batas 64 KiB dan foreign keys.
Replay exact diterima; perubahan checkpoint/output/target binding tidak menimpa receipt.

Crash sebelum receipt commit dapat meninggalkan artefak/dependency orphan yang identik
untuk retry. Acknowledgement yang hilang sesudah commit juga direplay tanpa inference.
Target yang sudah aborted menolak admission baru; `LoadGraphSourceBinding` tetap dapat
membaca receipt untuk audit. Reader memeriksa hash, bentuk canonical JSON dan konsistensi
kolom terhadap payload. Ia tidak memberikan live authority. Pemanggil mengautentikasi
akses corpus; belum ada CLI atau inventory ASSEMBLE yang otomatis memakai workflow ini.

Receipt ini membuktikan transform dan sumber, bukan freshness keputusan. Recorded revision
RESOLVE tidak boleh lebih besar dari target, tetapi nilai yang lebih kecil **belum** boleh
dipakai untuk melonggarkan guard GraphDelta. Receipt committed, dependency BIND/EXTRACT,
revalidasi kandidat dan reaffirmation lintas revision tetap gate terpisah. Prasyarat target
yang gagal pada commit dapat menyisakan orphan bounded; tidak ada job graph yang dijadwalkan.
Lihat [hasil verifikasi](verification-report-graph-source-receipt.md).

## Integrasi yang perlu diselesaikan

Gate BIND historis kini tersambung ke preparation: exact identity penerbit/regulasi/
pasal direkonstruksi dari dokumen bersumber dan lifetime-nya dibandingkan pada target
registry. Penambahan tak terkait boleh diterima tanpa menulis ulang revision di artefak.
[Laporan verifikasi](verification-report-document-registry.md) membatasi klaim ini;
gate belum mengesahkan alias/resolusi atau menggantikan recheck dalam admission atomik.

1. Baca RESOLVE output/checkpoint dan intent immutable; autentikasi EXTRACT/kandidat,
   receipt dan dokumen sumber. Untuk empty extraction, buktikan empty coverage dari
   source/counts dan checkpoint; jangan mengarang registry operation.
2. Ikat sumber ke base snapshot melalui artefak/receipt baru bila ingestion asal
   belum memiliki snapshot. Pertahankan original-to-bound mapping, bytes/hash asli,
   canonical/provision/source ID dan dependency. Jangan mengganti context/refs di
   artefak lama atau menganggap source tanpa snapshot otomatis anggota corpus.
   Transform envelope, membership initial-index, persistence artefak/dependency dan
   receipt graph sudah tersedia di atas; integrasikan dengan gate receipt/freshness
   sebelum menganggap sumber siap untuk penjadwalan ASSEMBLE.
3. Pilih satu registry revision publication. Receipt dari beberapa dokumen bisa
   berasal dari revision berbeda karena setiap CAS RESOLVE menaikkan revision global.
   Guard delta saat ini masih mensyaratkan revision sama. Sebelum memperluasnya ke
   `receipt_revision <= view_revision`, buktikan receipt historis, canonical tetap aktif
   dan bertipe sama, serta dependency lookup positif/negatif belum berubah pada view.
   Reader candidate view di atas sudah tersedia untuk konteks RESOLVE; dependency
   dari tahap sebelumnya dan bukti binding lintas tahap masih harus disambungkan.
   Bila lookup berubah, replan sumber terdampak atau gunakan reaffirmation durable
   yang eksplisit. Jangan mengganti recorded lookup/decision revision secara diam-diam.
4. Ekspor exact canonical view, simpan plan/view content-addressed, register dependency,
   lalu simpan seluruh inventory child jobs atomik. Replay harus memakai bytes/plan
   yang sama. Kegagalan antar-object boleh menyisakan orphan, tidak mengaktifkan job
   dengan sebagian input yang belum lengkap.
   Ekspor/persistence deterministik tersedia melalui `PrepareGraphAssembly`; inventory
   atomik dan penghubung admission dependency freshness tetap pekerjaan berikutnya.
5. Claim child ASSEMBLE, jalankan worker dengan deadline/cancellation, verifikasi delta
   terhadap plan dan source, kemudian commit checkpoint/output STAGED secara atomik
   dengan pemeriksaan ulang authority. Rekonsiliasi acknowledgement yang hilang.
6. Writer Neo4j menggunakan receipt/idempotency/visibility dan mempertahankan record
   bersama yang sudah ada. Publication baru aktif sesudah seluruh backend siap;
   closure incremental memerlukan before-image dan full-rebuild equivalence.

Ini mempertahankan desain multi-document Hybrid GraphRAG. Latency query tetap dipisahkan
dari bulk job; ukur p95/p99 SQL/queue/RPC/commit, recovery, memory, dan correctness
provenance sesuai [benchmark wajib](../configs/benchmark-targets.yaml). Status masih
**REQUIRED_UNMEASURED**; fixture dan read gate tidak membuktikan release lengkap.
