# src/server/internal/indexing

`graph_results_test.go` menguji collection output ASSEMBLE terhadap PostgreSQL
nyata: cancellation, fence, pin, stamp, pool satu koneksi dan cancellation saat
lock wait. Pipeline native kini melewati preparation seluruh output sebelum Neo4j
dan menolak hasil prepared lama setelah recovery mengganti checkpoint. Ini belum
publication gabungan; [laporan](../../../../doc/verification-report-graph-completed.md).

`graph_processor_restore_test.go` menguji cold restore/cache/registry refresh dan
pin cleanup pada PostgreSQL/Qdrant nyata dengan worker sentinel. Recovery terhadap
checkpoint ber-output hilang harus menjadi FAILED dan tidak diklaim ulang. Fixture
source kini memakai hash ontology JSONC asli; ini bukan live LLM atau Rust RPC run.

`graph_output_commit_test.go` melanjutkan fixture inventory ke transaksi output
ASSEMBLE pada PostgreSQL nyata: rollback seluruh metadata/checkpoint, cancellation,
registry movement saat lock wait, dan exact acknowledgement reconciliation. Output
metadata disintesis eksplisit; test tidak mengklaim Rust RPC/model nyata berjalan.

`graph_jobs_test.go` melanjutkan fixture published source ke admission/inventory/claim
graph dengan PostgreSQL/Qdrant nyata. Ia menguji rollback child insert, replay, caller
mutation, registry drift termasuk lock wait, source cancellation dan reclaim. Graph
execution tetap milik workflow/Rust; fixture ini tidak mengimplementasikan writer
graph di indexing dan tidak membuktikan source nonempty atau kualitas model.

`graph_dispatch_test.go` memeriksa handoff claim ke builder request terhadap registry,
publication takeover, checkpoint sumber dan live pin. Heartbeat extension tetap sah,
sedangkan expiry/owner/attempt palsu ditolak. Fixture ini belum menjalankan RPC Rust.

`graph_registry_fixture_test.go` menambah metadata/edisi BIND dan row identity
sintetis yang eksplisit ke fixture indeks sebelum registrasi. Pengujian preparation
memastikan perubahan key penerbit ditolak meski hash artefak/receipt tetap sama;
fixture ini tidak menjalankan ingestion/model atau membuktikan kualitas graph.

`graph_source_membership_test.go` memakai alur publication PostgreSQL/Qdrant nyata
untuk memverifikasi receipt sumber snapshot sebelum dipakai oleh graph preparation.
Kode assembly tetap milik domain/workflow; pengujian di sini membuktikan batas keluar
komponen indexing tanpa menambahkan algoritma graph ke writer indeks.
`graph_source_bindings_test.go` meneruskan fixture publication tersebut ke receipt
graph dan workflow persistence: race cancellation/released pin saat lock tertahan,
crash sebelum commit, lost acknowledgement, immutable replay dan audit sesudah abort.
EXTRACT/RESOLVE kosong dan checkpoint disintesis eksplisit; ini bukan uji kualitas LLM.
Fixture yang sama kini menguji plan/view ASSEMBLE tersimpan, replay, ontology drift,
dan cancellation setelah persistence. Varian default tidak menjalankan Rust.
`native_graph_test.go` menyediakan opt-in RPC Rust dengan FileStore bersama, dua
entitas/mention, satu assertion/support, commit PostgreSQL dan cold recovery tanpa
RPC ulang. `native_graph_source_test.go` menyiapkan EXTRACT/review sintetis tetapi
memakai allocator, alias lookup, intent, registry CAS dan checkpoint RESOLVE produksi.
Lihat [laporan native graph](../../../../doc/verification-report-native-graph.md)
untuk prasyarat dan batas hasil terhadap model/gold serta benchmark.
`native_graph_neo4j_test.go` menambahkan subtest opt-in yang menulis output Rust
tersebut ke Neo4j nyata dan memeriksa exact replay/seal: 8 record, 5 edge, 1 operation.
Helper ini menguji integrasi backend, belum publication graph PostgreSQL atau
traversal query; lihat [laporan Neo4j](../../../../doc/verification-report-neo4j.md).

`initial_snapshot.go` mengubah pilihan CHUNK terautentikasi menjadi snapshot awal
dan corpus-facts manifest, lalu mengikat sumber melalui receipt. Identitas
snapshot mencakup seluruh pilihan terurut; count graph nol eksplisit untuk
profil dense/BM25. Output menyediakan referensi population Rust, tanpa
menjadwalkan model atau menyatakan quality gate lulus.

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
CLI persiapan dan pengumpulan seluruh output publication kini tersambung melalui
`initial_bootstrap.go`, `initial_completed.go`, dan `initial_publish.go`.
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
durable, retry dan commit checkpoint fenced; daemon INDEX memakai processor/executor
yang menyusun library ini.

Berkas: [publication.go](publication.go), [initial_prepare.go](initial_prepare.go),
[initial_writer.go](initial_writer.go), [initial_artifacts_test.go](initial_artifacts_test.go),
dan [initial_writer_test.go](initial_writer_test.go).
Preparation mengautentikasi batch/plan/source/checkpoint/dictionary untuk daftar
sumber snapshot awal; writer menyimpan intent, menulis Qdrant, membaca ulang semua
point dan merekam receipt. Pemanggil wajib membekukan inventory dan backend wajib.
Lihat [kontrak writer](../../../../doc/initial-index-writer.md) untuk batas resource,
retry, namespace, dan prasyarat integrasi. CLI operator tersedia; route publik belum.

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
receipt graph hilang. Daemon INDEX opt-in tersedia; mutation Neo4j, incremental/compensation,
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

`initial_source_binding.go` mengautentikasi CHUNK lalu menyimpan envelope/receipt snapshot. `initial_job_recovery.go` memulihkan checkpoint sukses lama tanpa inference ulang. `initial_completed.go` mengumpulkan dan mengadmit seluruh output durable; `initial_publish.go` meneruskan profil dense/BM25 melalui writer dan publication, termasuk replay tanpa upsert. Graph requirement yang sudah staged tetap wajib. Integrasi, batas ukuran dan prasyarat CLI dijelaskan dalam [kontrak sumber/publication](../../../../doc/index-source-publication.md).

`initial_bootstrap.go` mengimpor statistik Rust yang terverifikasi, memeriksa
root dictionary registry, membentuk analyzer/model generation, mengadmit seluruh
sumber, lalu mempersist plan dan menjadwalkan inventory atomik. Dependency import
memakai exact replay; gagal admission dapat meninggalkan prerequisites immutable.
Tes PostgreSQL/FileStore memeriksa replay/model drift tanpa mengklaim DF atau
embedding fixture sebagai hasil kualitas model nyata.

`native_pipeline_test.go` menguji opt-in statistik/embedding yang benar-benar
dihitung Rust/C++, publication backend nyata dan query/hidrasi. Sumber tetap
fixture CHUNK; [laporan](../../../../doc/verification-report-native-index.md)
memisahkan integrasi ini dari gold dan acceptance performa.

`native_api_test.go` melanjutkan run tersebut melalui API HTTP nyata: cold query
concurrent, warm reuse, readiness, C01 response dan cleanup seluruh read lease.
Tes tidak mengklaim source fixture sebagai evaluasi legal/gold.
