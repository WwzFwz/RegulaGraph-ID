# src/server/cmd/cli

`review-alias -target-provisional -canonical ...` menampilkan occurrence sumber
dan occurrence pembentukan target sebelum acceptance. Mode ini tidak boleh
digabung dengan create-provisional atau target-document; label mengikuti profil
existing. [Panduan](../../../../doc/provisional-entity-review.md).

`review-alias -create-provisional` kini mengekspos review occurrence untuk tipe
semantik selain regulation/organization/provision. Mode ini menolak `-canonical`
dan `-target-document`, serta mempertahankan approval/replay eksplisit.
[Panduan provisional](../../../../doc/provisional-entity-review.md).

`review-alias` menyediakan inspect/accept source mention EXTRACT dan target BIND
existing. Actor berasal dari akun OS; acceptance memerlukan operation, hash rencana,
revision dan reason eksplisit. Input/output serta recovery ada di
[panduan](../../../../doc/sourced-alias-review.md); command tidak memanggil model
pada mode existing-target.

`abort-index -corpus ... -publication ...` menutup publication INDEX yang belum
published melalui coordinator publication setelah memeriksa ownership inventory.
Memerlukan `REGULAGRAPH_POSTGRES_DSN` dan inventory yang sudah admitted; bukan
perintah abort reservation sebelum inventory tersedia. Deadline default satu
menit, maksimum lima menit. Ledger backend planned/applied menghalangi abort;
command ini tidak melakukan compensation, menghapus artefak atau mereset job.
Snapshot published dan replay pada snapshot yang sudah ABORTED ditolak. Bila
stdout gagal setelah commit, periksa state durable sebelum tindakan berikutnya.
Replan setelah abort memakai identitas publication/generation baru dan dependency
snapshot yang dibekukan ulang. [Panduan](../../../../doc/index-source-publication.md).

`migrate -dir migrations -timeout 5m` menerapkan migration PostgreSQL secara
eksplisit memakai DSN environment dan runner checksum/advisory-lock yang sama.
Command tidak menyediakan reset/down atau menjalankan migrasi saat startup.
Output `migration_files` menghitung inventory termasuk file yang sudah diterapkan,
bukan jumlah perubahan baru. [Panduan](../../../../migrations/README.md).

`submit -acquisition-record ... -acquisition-root ...` kini mengimpor PDF collector
secara terverifikasi sebelum enqueue, dengan root `REGULAGRAPH_ARTIFACTS_DIR`
bersama worker. `submit_acquisition.go` memuat record berbatas; workflow memiliki
verifikasi/copy/registration. Request template harus tanpa sources/observations.
[Panduan impor](../../../../doc/acquisition-import.md) memuat replay dan batas.

`prepare_graph.go` menyediakan `prepare-graph` untuk seluruh sumber snapshot indeks
awal yang sudah RESOLVE, dengan base/scope eksplisit dan child scheduling atomik.
Tidak ada keputusan model/review otomatis; [panduan](../../../../doc/graph-preparation.md).

`publish_graph.go` menyediakan `publish-graph` untuk inventory ASSEMBLE lengkap.
Corpus, reservation snapshot, graph generation, auth scope, ontology dan backend
routes harus eksplisit. Command memanggil coordinator graph+index bersama,
menolak partial success dan memeriksa durable replay tanpa model/embedding ulang.
Source preparation/scheduling dilakukan oleh `prepare-graph` sebelum worker.
[Panduan](../../../../doc/graph-publication.md) memuat dependency dan retry.

`query-evidence` menerima `graph` dan `hybrid-graph` dengan file route/policy
terpin; graph-only tanpa reranker tidak membuka native inference. Command
default evidence-only; `-answer` mengaktifkan generator lokal terpin dan draft
bersitasi dari snapshot yang sama. `query_answer.go` memvalidasi C01 output dan
menolak promosi status model menjadi verified. Scope operator berasal dari konfigurasi, bukan pertanyaan.
Lihat [penggunaan](../../../../doc/query-evidence.md).
Konfigurasi model/tokenizer/template ada pada [jawaban lokal](../../../../doc/local-answer.md).

`semantic_review.go` menyediakan `review-resolution` untuk inspeksi dan acceptance
batch LINK/DEFER exact. Akun OS menjadi identitas audit; corpus/scope dan kredensial
operator menjadi batas akses. Approval belum berarti registry committed/published;
lihat [panduan](../../../../doc/semantic-review.md).

`prepare_snapshot.go` menyediakan `prepare-snapshot` untuk mengekspor snapshot
dan source refs C01 dari pilihan job/CHUNK terdaftar. Scope, hash dan authority
diperiksa indexing; output memakai direktori baru tanpa overwrite. Lihat
[panduan persiapan](../../../../doc/index-source-publication.md).

`prepare_index.go` menyediakan `prepare-index`: membaca ekspor snapshot, refs
dictionary/statistik C01 dan manifest embedding JSON terpin, lalu memanggil
bootstrap indexing untuk menjadwalkan semua child. Manifest model biner beserta
hash diekspor untuk worker Rust; existing file harus identik saat replay.
Output `scheduled` belum berarti search-ready. Selanjutnya coordinator INDEX
opt-in, `publish-index`, dan `query-evidence` memakai snapshot/generation sama.

Entry point perintah ingestion, update, serta query melalui komponen Go yang sama. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Perintah collect memanggil internal/workflows.CollectSources dan mengunduh PDF nyata serta metadata ke data/acquisition. Perintah audit memanggil sources.AuditAcquisition untuk memverifikasi inventory lokal secara streaming dan menulis manifest deterministik. Perintah `submit` menerima request ProtoJSON berbatas, memasang hash ontology dan policy corpus terpin, lalu membuat job PostgreSQL yang idempotent melalui scheduler. CLI hanya memuat argumen, merakit dependency, dan melaporkan output JSON. Exit code 0 berarti operasi/integrity sukses, 1 berarti kegagalan atau integrity error, dan 2 berarti argumen salah. CLI evidence-only tersedia; query jawaban serta update produksi belum tersedia. Lihat [panduan collector](../../../../doc/acquisition.md).

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [main.go](main.go), [main_test.go](main_test.go), [audit.go](audit.go), [audit_test.go](audit_test.go), [submit.go](submit.go), dan [submit_test.go](submit_test.go).

Untuk submit, set `REGULAGRAPH_POSTGRES_DSN`, `REGULAGRAPH_ONTOLOGY_PATH`, `REGULAGRAPH_ONTOLOGY_SHA256`, `REGULAGRAPH_CANDIDATE_POLICY_PATH`, dan `REGULAGRAPH_CANDIDATE_POLICY_SHA256`, lalu jalankan `go run ./src/server/cmd/cli submit -request request.json -job-id job:example`. Hash SHA-256 merujuk byte file tepat, bukan nilai yang sudah diparse. Request memuat corpus ID, referensi source blob, operasi, idempotency key, dan producer manifest. Tanpa opsi impor acquisition, pemanggil harus menyiapkan blob terdaftar; bentuk referensi diperiksa CLI dan integritasnya diperiksa downstream. URL ditolak sampai stage ACQUIRE mempunyai dispatcher; Opsi acquisition di atas menyediakan verifikasi/copy dan registration dari record collect lengkap sebelum enqueue. Output JSON menyatakan job ID, corpus, state, stage, dan apakah idempotent replay. Status queued bukan bukti pipeline selesai. Contoh bentuk catalog policy ada di [integrasi resolusi](../../../../doc/semantic-resolution.md).

Perintah discover menerima seed katalog dan memanggil workflows.DiscoverSources untuk menghasilkan antrean persisten tanpa unduhan PDF. Input collect tetap URL detail/PDF; jangan menukar kedua jenis file. Lihat configs/listings.txt untuk seed katalog.

## Benchmark dan perhatian performa

Bila `REGULAGRAPH_RESOLUTION_PRODUCER_PATH` atau `REGULAGRAPH_RESOLUTION_PRODUCER_SHA256` diisi, `submit` memerlukan keduanya, memvalidasi file producer RESOLVE, dan menambahkan hash byte file ke request durable tanpa duplikasi. Ini memungkinkan daemon menolak pergantian model/config pada job lama. File dapat diekspor dari gateway sesuai [panduan RESOLVE](../../../../doc/semantic-resolution.md); request tanpa pin ini tetap dapat melalui dokumen/EXTRACT tetapi ditolak executor RESOLVE opt-in.

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Perintah discover, collect, audit inventory D01, dan submit job durable berpolicy terpin sudah aktif. Kontrak/validator C01, evaluator E01, serta fondasi storage S01 juga tersedia pada komponen pemiliknya. Submit belum mengaktifkan ACQUIRE, dispatch RESOLVE–INDEX, update/query end-to-end, atau publication; audit integrity tidak membuktikan kualitas isi PDF, canonical identity, atau target performa.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [main.go](main.go) | Pertahankan routing command tipis; expose job/status/update/query hanya setelah workflow pemilik aktif. | Uji exit code, machine-readable output, cancellation, dan Ctrl+C pada proses aktif. |
| [audit.go](audit.go) | Pertahankan flag bounded dan exit integrity; tambahkan opsi output hanya bila format manifest tetap kompatibel. | Uji root/write/stdout failure, cancellation, dan invalid inventory tidak pernah exit 0. |

[query.go](query.go) menyediakan `query-evidence` untuk operator lokal: pertanyaan,
tanggal AS_OF dan profil eksplisit masuk ke RAGSession, native embedding, dense/BM25
serta hidrasi sumber. Output adalah evidence ProtoJSON dengan penolakan eksplisit,
bukan jawaban model. Konfigurasi, prasyarat snapshot terpublikasi, cold setup dan
exit code dijelaskan di [panduan query](../../../../doc/query-evidence.md).
[query_test.go](query_test.go) menguji validasi sebelum I/O, redaction dan output.
`demo.go` merakit baseline interview lokal dari export sampel offline. Default
http://127.0.0.1:8096 memakai profil Ollama regulagraph-demo:latest; `-model=`
menjalankan pencarian saja. Ini tidak membuat publication atau menggantikan jalur
query-evidence terpin. [Panduan](../../../../doc/interview-demo.md) memuat setup.

Reranking native dapat diaktifkan untuk query-evidence dengan manifest ProtoJSON dan
pin hash pada `REGULAGRAPH_QUERY_RERANK_MANIFEST` dan
`REGULAGRAPH_QUERY_RERANK_MANIFEST_SHA256` sesuai panduan query. Kedua nilai wajib
bersama. [query_reranking.go](query_reranking.go) mengekspor skor/model C01 dan
memeriksa kesamaan urutan bukti; query_reranking_test.go menguji accounting dan
drift. Tanpa konfigurasi ini, hasil tetap baseline fusion yang eksplisit.

`prepare-dictionary` menerima vocabulary UTF-8/hash terpin dari worker Rust,
menggunakan allocator PostgreSQL lewat indexing, dan menulis ArtifactRef biner
ke file baru. Ia memerlukan DSN dan root artefak yang sama; tidak menjalankan
migrasi atau publication. [Panduan populasi](../../../../doc/lexical-population.md)
menjelaskan argumen, replay dan batas penggunaan.

`publish_index.go` menambahkan `publish-index -corpus ... -publication ... -profile hybrid` untuk inventory durable yang sudah lengkap. Ia merakit dependency dan memanggil publisher dense/BM25; scope corpus/endpoint harus cocok dan tidak menurunkan graph requirement yang sudah staged. [Panduan](../../../../doc/index-source-publication.md) menjelaskan environment, replay, exit code serta persiapan inventory yang masih perlu dirangkai.
