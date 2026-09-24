# src/server/internal/adapters/postgres

Adapter penyimpanan metadata dokumen, versi, manifest ingestion, dan status workflow pada PostgreSQL. Adapter Go memakai koneksi yang dipakai ulang. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menyimpan kebijakan ranking atau menggantikan Neo4j sebagai implementasi traversal. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menangani transaksi lokal, keunikan ID, pagination, dan pool koneksi. Transaksi PostgreSQL tidak dianggap mencakup Qdrant atau Neo4j; perubahan schema mengikuti migrations.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[repository.go](repository.go) mengelola lifecycle pool dan error boundary. [migrate.go](migrate.go) menerapkan migration terurut dengan advisory lock serta checksum. [jobs.go](jobs.go) mengelola idempotency, claim PARSE/STRUCTURE/BIND/CHUNK/EXTRACT/RESOLVE, attempt global dan budget retry per stage, lease/fence, polling cancellation, checkpoint, dan completion atomik yang memberi prioritas pada cancellation. Klaim RESOLVE mensyaratkan checkpoint EXTRACT dengan terminal sukses dan tidak diambil claimant generik. [artifacts.go](artifacts.go) mengikat serta memuat metadata immutable untuk handoff checkpoint. [registry.go](registry.go) mengalokasikan exact canonical identity secara revisioned dan idempotent. [registry_aliases.go](registry_aliases.go) meregistrasikan profil/alias bersumber secara append-only dengan CAS revision, sedangkan [registry_candidates.go](registry_candidates.go) membaca kandidat ambigu dan revision lookup positif/negatif dalam satu snapshot. [publication.go](publication.go) merealisasikan reservation, backend receipt, snapshot CAS, outbox, abort, dan read lease. [repository_integration_test.go](repository_integration_test.go) dan [registry_aliases_integration_test.go](registry_aliases_integration_test.go) adalah suite PostgreSQL aktual dan akan skip jika DSN test tidak tersedia.

[registry_candidate_batch.go](registry_candidate_batch.go) membaca scope yang dipilih eksplisit per mention
dalam satu transaksi lookup, memeriksa key/revisi serta closure alias, kemudian meneruskan observasi ke
builder artefak C01. Nol mention menghasilkan `ErrNoCandidateMentions` tanpa pembacaan registry agar workflow
dapat melewati RESOLVE secara eksplisit. [registry_candidate_batch_test.go](registry_candidate_batch_test.go)
memeriksa mapping dan hasil tidak mungkin dengan fixture; belum membuktikan transaksi PostgreSQL nyata.

[registry_semantic.go](registry_semantic.go) menerima proposal LINK/DEFER terikat batch kandidat dan melakukan
CAS revision dalam transaksi serializable. [registry_semantic_inputs.go](registry_semantic_inputs.go) memeriksa
hash/metadata artefak terdaftar serta baris review LINK yang mengikat proposal/kandidat persis secara batch;
[registry_semantic_fence.go](registry_semantic_fence.go) mengunci job/checkpoint dan memeriksa corpus,
owner, fence, cancellation, lease, manifest EXTRACT, dan hash sumber di transaksi yang sama dengan
CAS. [registry_semantic_replay.go](registry_semantic_replay.go) merekonstruksi receipt lama dengan pemeriksaan
integritas row/payload. [registry_semantic_integration_test.go](registry_semantic_integration_test.go)
menguji writer ini pada PostgreSQL disposable, termasuk retry, stale candidate, forgery, konkurensi,
handoff FileStore nyata, dan lease yang berubah di tengah pembacaan/transaksi. Caller wajib memakai
`SemanticResolutionHandoff` agar byte berasal dari `ReadVerified`. [registry_semantic_intent.go](registry_semantic_intent.go) menyimpan intent append-only sebelum CAS agar retry membawa request/review yang sama; [registry_semantic_empty.go](registry_semantic_empty.go) membaca revisi terikat lease untuk EXTRACT tanpa mention. Producer review terautentikasi belum tersedia dan adapter ini belum menjadi stage RESOLVE publik.

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

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [repository.go](repository.go) dan [migrate.go](migrate.go) | Pertahankan pool eksplisit dan migration checksum; tambahkan rollout migration hanya melalui file bernomor baru. | Uji schema kosong, replay, checksum drift, timeout, serta upgrade snapshot produksi. |
| [jobs.go](jobs.go) | Pertahankan claim PARSE/STRUCTURE/BIND/CHUNK/EXTRACT, cancellation fenced, dan retry per-stage; tambah lease heartbeat serta API cancellation. | Failure injection pada expiry/renewal/checkpoint, verifikasi stage ownership dan attempt monotonic, lock contention, serta ukur queue time dan pool saturation. |
| [registry.go](registry.go) | Pertahankan atomic exact-key allocation, operation ledger, historical replay, dan revision CAS; tambah merge/split/review hanya melalui keputusan revisioned. | Uji serialization retry, concurrent same-key allocation, stale revision, partial restore/corruption, legacy upgrade, serta p50/p95/p99 batch. |
| [registry_aliases.go](registry_aliases.go) dan [registry_candidates.go](registry_candidates.go) | Hubungkan producer bersumber dan stage RESOLVE setelah validasi keberadaan `support_refs`; tambahkan review, closure, dan query historis melalui keputusan revisioned. | Uji candidate coverage dan false merge/split pada gold, replay/corruption, concurrent write, ukuran indeks, pool wait, serta p50/p95/p99 lookup/batch. |
| [artifacts.go](artifacts.go) | Gunakan dependency rows untuk closure U01 dan batch registration. | Bandingkan closure incremental dengan rebuild dan ukur reverse lookup pada corpus referensi. |
| [publication.go](publication.go) | Sambungkan backend operations, compensation, retention, dan recovery U01/O01. | Injeksi crash di setiap langkah, verifikasi historical visibility, read lease, dan pool saturation. |
