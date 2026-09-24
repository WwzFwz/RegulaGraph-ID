# src/server/internal/workflows

Orchestration alur ingestion, pembaruan incremental, dan tanya jawab. Folder ini mengatur urutan tahap, percabangan, checkpoint, retry, dan pelaporan status. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menjadi tempat implementasi parser, retriever, atau SDK database. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak menyuntikkan dependency melalui kontrak yang eksplisit dan membawa run ID serta snapshot ID. Retry harus idempotent, publikasi lintas penyimpanan memakai mekanisme yang dirancang, dan kegagalan parsial tetap terlihat.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [answer.go](answer.go), [bind.go](bind.go), [bind_test.go](bind_test.go), [ingest.go](ingest.go), [ingest_test.go](ingest_test.go), [update.go](update.go), [parse.go](parse.go), [parse_test.go](parse_test.go), [collect.go](collect.go), [collect_test.go](collect_test.go), [discover.go](discover.go), [discover_test.go](discover_test.go), [semantic_resolution.go](semantic_resolution.go), dan [semantic_resolution_test.go](semantic_resolution_test.go).

collect menjalankan batch acquisition D01 melalui adapter sources dengan deduplikasi URL, jumlah worker terbatas, cancellation, progress, serta hitungan sukses/reuse/gagal. Scheduler pada ingest.go membuat job persisten S01 dan memilih `PARSE` bila semua source sudah berupa blob; URL tetap dimiliki `ACQUIRE`. `parse.go` merotasi claim PARSE/STRUCTURE/CHUNK/EXTRACT, hanya mengirim setiap tahap dari checkpoint pendahulunya yang sukses, membatasi RPC pada deadline lease, meneruskan cancellation durable, membaca ulang bytes output terverifikasi, menyimpan dependency manifest, lalu menyimpan checkpoint. Pada PARSE, observation hanya diteruskan bila corpus, portal, dan source-blob hash cocok dengan locator request. Hasil parsial masuk `WAITING_REVIEW`; hasil lengkap bergerak melalui handoff berikutnya. [Panduan collector](../../../../doc/acquisition.md) menjelaskan acquisition.

discover.go menyimpan checkpoint discovery.json dan antrean queue.txt setelah setiap halaman baru; satu proses penulis per direktori. Seed diikuti breadth-first dengan batas halaman per run, URL dideduplikasi, error dipertahankan dan dicoba ulang pada run berikutnya. Checkpoint adalah inventaris D01 lokal, bukan durable job produksi. Hasil discover tidak membuktikan ketersediaan PDF atau canonical identity.

`SemanticResolutionHandoff` menerima lease RESOLVE yang sudah diklaim, membandingkan checkpoint sukses EXTRACT dan metadata artefak kandidat terdaftar, lalu membaca kedua byte lewat `ReadVerified` dalam budget gabungan sebelum memanggil writer PostgreSQL dengan fence. Writer memverifikasi ulang lease/checkpoint pada transaksi CAS. [semantic_resolution_output.go](semantic_resolution_output.go) memeriksa batch dan menyimpan intent immutable sebelum CAS; setelah receipt cocok, ia menyimpan `ResolutionBatch`, dependency manifest, checkpoint terminal, dan menyelesaikan attempt. Retry setelah crash memakai intent yang sama; kandidat/review permanen stale membuat job `FAILED` agar rencana baru dapat dibuat dengan job baru. Cancellation atau lease yang kalah tidak dilaporkan sebagai permintaan replan. [semantic_resolution_empty.go](semantic_resolution_empty.go) menangani EXTRACT lengkap tanpa mention tanpa membuat keputusan registry. Handoff belum membuat kandidat/proposal atau mengautentikasi reviewer.

[semantic_resolution_candidates.go](semantic_resolution_candidates.go) kini membuat dan menyimpan kandidat dari rencana scope exact yang diberikan caller. Ia memeriksa lease/checkpoint dan perubahan revision di sekitar lookup; artefak berhash serta dependency manifest terdaftar sebelum dipakai writer. Pemilihan scope/normalisasi legal, proposal, identitas reviewer, dan dispatch stage produksi tetap perlu producer tersendiri.

## Benchmark dan perhatian performa

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

**ANSWER.** Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

`parse.go` memeriksa ontology hash pada request EXTRACT tersimpan dan manifest producer, lalu menerapkan gate typed graph setelah closure sumber/bukti. Mismatch menjadi kegagalan deterministik sebelum registrasi artefak.

Collector D01, kontrak/validator C01, scheduler durable S01, dispatch PARSE/STRUCTURE/CHUNK/EXTRACT ke worker Rust, dan executor BIND Go sudah aktif. BIND memverifikasi artefak/checkpoint STRUCTURE, menjalankan exact registry issuer/regulasi/pasal dalam batch idempotent, menulis dependency manifest dan output immutable, lalu memisahkan hasil lengkap dari review parsial. CHUNK memeriksa stage-owned records, completeness, typed reference closure, serta containment span terhadap versi dan struktur. EXTRACT hanya menerima CHUNK lengkap, mengikat model/prompt ke config request, memeriksa accounting/provenance, serta menghidrasi evidence text berbatas untuk exact mention dan boundary UTF-8 sebelum commit. Kegagalan baca storage sementara tetap `RETRY_WAIT`; korupsi immutable dan closure invalid menjadi terminal. Retry transient memakai exponential backoff durable dengan budget terpisah per stage; attempt global dan fence tetap monotonik. Klaim, fenced commit, output/checkpoint, dan recovery RESOLVE tersedia sebagai workflow yang menerima kandidat/proposal/review terpin; producer serta dispatch produksi, lease renewal batch panjang, jitter retry, answering, dan benchmark end-to-end masih mengikuti paket berikutnya.

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

[semantic_resolution_model.go](semantic_resolution_model.go) menyediakan `ProposeWithModel`: hydrate konteks dari EXTRACT/document/text terverifikasi, panggil gateway model, validasi proposal/revision, dan simpan request-response immutable untuk replay lintas restart. [semantic_resolution_evidence.go](semantic_resolution_evidence.go) mengambil seluruh support alias kandidat dari katalog checkpoint EXTRACT sukses dalam corpus/snapshot/auth yang sama, lalu memverifikasi ulang bytes serta closure dokumen. Cache teks/budget dipakai bersama dan dependency identik dideduplikasi; LINK wajib merujuk konteks mention serta kandidat terpilih. Hasilnya dapat diteruskan ke handoff registry yang sudah ada; metode ini tidak mengautentikasi reviewer atau menjalankan dispatch otomatis. Scope planner masih perlu diselesaikan. Lihat [integrasi resolusi](../../../../doc/semantic-resolution.md).

`parse.go` memakai `SaveExtractionCheckpoint` untuk output EXTRACT yang telah divalidasi, termasuk recovery. Checkpoint sukses dan katalog mention disimpan dalam satu transaksi fenced. Output parsial/gagal tetap menyimpan outcome tanpa memasukkan lokasi bukti sebagai sumber kandidat.
