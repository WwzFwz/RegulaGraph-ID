# Dispatch dan admission output ASSEMBLE

Dokumen ini menjelaskan library Go yang menjalankan batch ASSEMBLE dan memeriksa
GraphDelta terhadap input terverifikasi. Rust memiliki transformasi canonical;
Go memiliki pemeriksaan hasil sebelum commit. Status verified di sini belum berarti
checkpoint tersimpan, job STAGED, atau graph terpublikasi.

## Alur dan kontrak

`workflows.ExecuteGraphAssembly` menerima authority dari admission inventory, reader
artefak, port batch worker, pin snapshot, claim job, request context dan ontology.
Authority diperiksa sebelum dispatch. Builder mengikat empat role input pada plan;
reader memverifikasi bytes/hash/size plan, dokumen, EXTRACT, RESOLVE, registry view
dan normalized text yang dipakai mention/support. Stored plan harus sama dengan
assignment. RPC memakai deadline terpendek dari caller, pin dan claim; cancellation
diteruskan melalui context.

Respons melewati `domain.ValidateGraphWorkerEnvelope`: correlation/job/attempt/fence,
stage, satu output graph sukses, checksum/checkpoint binding, schema dan producer
harus cocok. Unknown response/checkpoint fields, premature checkpoint visibility,
output kosong/oversized dan role lain ditolak. Output kemudian dibaca dengan hash/size
terverifikasi dan melewati `ValidatePlannedGraphDelta`.

Physical `ArtifactRef.artifact_id` hasil FileStore Rust dapat berbentuk
`artifact:graph-delta:<hash>`, sedangkan `GraphDelta.meta.record_id` adalah logical
`plan.output_artifact_id`. Keduanya tidak harus sama. Checkpoint mengikat physical
reference dan hash; payload mengikat logical plan ID. Source/plan role tidak boleh
dipakai ulang sebagai physical output ID.

Setelah admission delta, authority diperiksa lagi dan assignment harus tetap exact.
`VerifiedGraphOutput` memiliki salinan data; accessor juga mengembalikan salinan.
Library belum mendaftarkan output, menyimpan checkpoint atau menjalankan publication.
Commit wajib mengulang live fence/cancellation/source/publication di bawah lock;
read pasca-RPC tidak memberi izin yang berlaku selamanya.

## Pemeriksaan isi delta

Validator menerima C01 decoded inputs dan bytes normalized text. Pemanggil wajib
membuktikan registration, hash/size, receipt registry dan authority. Validator memeriksa
closure dokumen, EXTRACT/RESOLVE, exact registry selection, canonical type, ontology/hash,
schema, source context, kelengkapan, UTF-8 boundaries dan kesamaan mention dengan teks.

Projection expected mempertahankan entities, mentions dan keputusan sumber. Assertion
endpoint serta qualifier mention diganti hanya melalui keputusan canonical yang sah.
ID assertion/support memakai hash-v1 yang sama dengan Rust: SHA-256 dari domain ID,
byte NUL dan protobuf canonical tanpa record ID/visibility. Qualifier dan compare dates
diurutkan/dideduplikasi menurut bytes, numeric negative zero dinormalisasi, dan exception
DAG diremap secara iteratif. Support mempertahankan source/evidence/model/review serta
independent-source group. Perubahan format hash memerlukan review kompatibilitas.

Seluruh record diberi target visibility dan harus unik lintas tipe. Union dependency
EXTRACT/RESOLVE, empat role dan ontology harus exact, termasuk lookup kosong. Konflik
hash/revision ditolak. Delta dibandingkan penuh terhadap projection, sehingga missing
support, fakta tambahan, perubahan predicate/arah, keputusan hilang, report palsu atau
closure/profile/alias yang tidak diminta ditolak. Jalur ini khusus upsert satu sumber;
incremental removal tidak disimpulkan dari record yang absen.

## Resource dan pengujian

Input worker plan+empat role+teks tetap dibatasi 16 MiB; output maksimal 16 MiB
secara terpisah. Validator juga membatasi aggregate decoded input/output dan jumlah
map teks, termasuk entries kosong. Wire size bukan peak RSS. Pemeriksaan projection
menambah CPU/hashing Go; ukur terpisah dari assembly Rust, beserta read/RPC, queue
p95/p99, RSS dan cancellation lag. Tidak ada model call tambahan.

`assembly_tests.rs` mengekspor fixture worker melalui `REGULAGRAPH_GRAPH_FIXTURE_DIR`.
Go menjalankan `TestGraphOutputFromRustWorker` dan `TestExecuteGraphAssemblyWithRustArtifacts`
dengan path tersebut. Test rich Rust mencakup qualifier permutation/dedup, numeric -0,
mention qualifier, compare dates, exception DAG dan assertion/support dedup. Jalankan
test export dasar dan rich ke direktori berbeda agar output tidak saling menimpa.

Workflow tests memakai actual Rust-produced bytes tetapi authority/RPC ports sintetis.
Itu membuktikan return boundary, bukan jaringan worker bersama PostgreSQL. Lanjutkan
commit checkpoint/STAGED/recovery, daemon dan real-RPC integration; Neo4j serta benchmark
release tetap terbuka. [Laporan](verification-report-graph-output.md) menyimpan bukti
scoped PASS. Target [required](../configs/benchmark-targets.yaml) tidak berubah.
