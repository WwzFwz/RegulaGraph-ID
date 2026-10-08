# Coordinator ASSEMBLE: authority dan handoff

Dokumen ini menjelaskan batas authority Go di depan [worker ASSEMBLE](assembly-worker.md)
dan pekerjaan integrasi berikutnya. Reader/gate PostgreSQL di bawah sudah tersedia;
persiapan plan/view, inventory job, dispatch dan commit output graph belum dirangkai
menjadi workflow lengkap. Semua akses dilakukan caller yang telah mengautentikasi corpus.

## Authority yang tersedia

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

## Integrasi yang perlu diselesaikan

1. Baca RESOLVE output/checkpoint dan intent immutable; autentikasi EXTRACT/kandidat,
   receipt dan dokumen sumber. Untuk empty extraction, buktikan empty coverage dari
   source/counts dan checkpoint; jangan mengarang registry operation.
2. Ikat sumber ke base snapshot melalui artefak/receipt baru bila ingestion asal
   belum memiliki snapshot. Pertahankan original-to-bound mapping, bytes/hash asli,
   canonical/provision/source ID dan dependency. Jangan mengganti context/refs di
   artefak lama atau menganggap source tanpa snapshot otomatis anggota corpus.
3. Pilih satu registry revision publication. Receipt dari beberapa dokumen bisa
   berasal dari revision berbeda karena setiap CAS RESOLVE menaikkan revision global.
   Guard delta saat ini masih mensyaratkan revision sama. Sebelum memperluasnya ke
   `receipt_revision <= view_revision`, buktikan receipt historis, canonical tetap aktif
   dan bertipe sama, serta dependency lookup positif/negatif belum berubah pada view.
   Bila lookup berubah, replan sumber terdampak atau gunakan reaffirmation durable
   yang eksplisit. Jangan mengganti recorded lookup/decision revision secara diam-diam.
4. Ekspor exact canonical view, simpan plan/view content-addressed, register dependency,
   lalu simpan seluruh inventory child jobs atomik. Replay harus memakai bytes/plan
   yang sama. Kegagalan antar-object boleh menyisakan orphan, tidak mengaktifkan job
   dengan sebagian input yang belum lengkap.
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
