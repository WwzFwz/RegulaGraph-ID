# src/server/internal/indexing

Koordinasi commit batch indeks dan penerbitan snapshot dalam Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Worker Rust mempersiapkan record dan inference menghasilkan vector; adapter Go menulis indeks dan metadata. Satu coordinator mengatur publication marker.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

[initial_jobs.go](initial_jobs.go) mengekspor inventory child job stabil dan
memulihkan plan melalui admission sumber/dictionary ulang. PostgreSQL menyimpan
inventory atomik; `VerifiedIndexOutput.Commit` menutup checkpoint/STAGED atomik.
[initial_job_processor.go](initial_job_processor.go) menghubungkan admitted
inventory, RPC worker, registrasi dan commit untuk executor workflow/daemon.
Cache satu inventory tidak menghilangkan pemeriksaan authority dan byte per job.
CLI persiapan serta pengumpulan semua output publication masih perlu dihubungkan.
Kontrak dan batasnya ada pada [inventory INDEX](../../../../doc/index-job-inventory.md).

[initial_planning.go](initial_planning.go) membentuk plan INDEX deterministik dari
inventory CHUNK yang dipilih pemanggil tepercaya. Seluruh chunk dibagi tepat sekali,
checkpoint/snapshot/scope diperiksa, dan jumlah populasi statistik harus cocok.
[initial_plan_storage.go](initial_plan_storage.go) menyimpan byte plan beserta
dependency sumber/lexical, mendukung replay registrasi, lalu menuntut keluaran untuk
setiap plan tanpa substitusi atau duplikasi. Pemeriksaan keluaran memakai ulang
cache byte terverifikasi dalam satu budget agregat 64 MiB; ini bukan batas peak RSS.
[initial_dispatch.go](initial_dispatch.go) membangun request dari claim INDEX milik
scheduler, memanggil worker, dan mengautentikasi response/byte/context sebelum output
bisa diregistrasikan. Pemanggil tetap memiliki penyimpanan lease-plan, cancellation
durable, retry dan commit checkpoint fenced; library ini belum daemon INDEX.

Berkas: [publication.go](publication.go), [initial_prepare.go](initial_prepare.go),
[initial_writer.go](initial_writer.go), [initial_artifacts_test.go](initial_artifacts_test.go),
dan [initial_writer_test.go](initial_writer_test.go).
Preparation mengautentikasi batch/plan/source/checkpoint/dictionary untuk daftar
sumber snapshot awal; writer menyimpan intent, menulis Qdrant, membaca ulang semua
point dan merekam receipt. Pemanggil wajib membekukan inventory dan backend wajib.
Lihat [kontrak writer](../../../../doc/initial-index-writer.md) untuk batas resource,
retry, namespace, dan prasyarat integrasi. Tidak ada route publik/CLI baru.

Preparation membedakan alamat fisik `DocumentBatch`/`IndexBatch` keluaran worker
dari logical record ID di dalam payload. Plan dan artefak lexical tetap memakai
identitas typed yang persis. Media type, registered reference, hash/size, corpus,
checkpoint dan plan output ID tetap wajib; penerimaan alamat berbasis hash tidak
mengizinkan batch dengan ID keluaran di luar plan. Tes integrasi menjalankan kedua
bentuk alamat sampai publication/hydration; model dan teks tetap fixture sintetis.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Publication coordinator S01 aktif untuk reserve, stage, acknowledge, pre-commit
validation, snapshot CAS, dan abort melalui durable store. Writer Qdrant snapshot
awal kini diuji dengan PostgreSQL/Qdrant nyata, termasuk lost reply/retry dan
receipt graph hilang. Wiring daemon, mutation Neo4j, incremental/compensation,
serta benchmark indexing/retrieval masih mengikuti X01/U01. Fixture sintetis
tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [publication.go](publication.go) | Sambungkan batch mutation X01 serta compensation/reconcile/retire U01/O01 pada state machine yang sudah aktif. | Pertahankan VerifyPublicationReady sebelum commit; injeksi crash setiap backend dan tolak fence/receipt stale. |

`initial_writer_test.go` juga menguji pembacaan katalog, batas byte, korupsi teks,
unresolved policy dan lease yang dilepas. [published_rag_test.go](published_rag_test.go)
menyambungkan publication PostgreSQL/Qdrant nyata sampai draft bersitasi melalui
RAGSession dan SourceHydrator. Embedding/reranker/generator serta token counter sintetis,
Versi tes awal memakai store fixture yang sudah dibuat; pembaruan berikut
menguji factory berbasis katalog. Kualitas model belum diukur. Lihat [laporan](../../../../doc/verification-report-pinned-evidence.md).

Tes published RAG kini memanggil PreparePublishedQuery untuk memilih store dari
binding katalog, memuat BM25 terdaftar, dan menjalankan hybrid search. Resource
prepared yang sama dipakai kembali untuk query evidence-only di bawah lease baru.
HTTP Qdrant dan PostgreSQL nyata; embedding/reranker/generator tetap sintetis. Ini belum
menjalankan executable CLI dengan proses native nyata atau corpus PDF pengguna.

[lexical_dictionary.go](lexical_dictionary.go) mengalokasikan vocabulary dalam
halaman registry dengan operasi deterministik, mengekspor revision terpin, dan
menyimpan/mendaftarkan dictionary immutable. Retry setelah interupsi registrasi
tidak mengganti ID. lexical_dictionary_test.go menguji PostgreSQL/FileStore nyata
dan artifact statistik Rust yang memakai mapping Go. Membership sumber dan
statistik tetap harus terikat inventory coordinator sebelum publication.
