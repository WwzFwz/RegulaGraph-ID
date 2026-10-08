# Assembly GraphDelta berbukti

Dokumen ini menjelaskan library Rust `assembly/delta.rs` yang mengomposisikan
validasi EXTRACT terhadap dokumen, resolusi canonical, dedup assertion/support dan
pembentukan GraphDelta C01. Ini tahap transformasi batch; coordinator Go tetap
memiliki autentikasi artefak/registry, dispatch, fence, backend receipt dan publication.

## Input dan kontrak kepercayaan

`DeltaInput` menerima DocumentBatch, EXTRACT dan RESOLVE, ref EXTRACT/RESOLVE/registry,
entitas registry pada revision yang sama dengan RESOLVE, teks normalisasi yang dipakai
mention/support, producer ASSEMBLE, ID delta dan target sequence. Snapshot basis diambil
dari context EXTRACT; DocumentBatch/EXTRACT/RESOLVE harus konsisten pada snapshot,
corpus, auth scope dan config fingerprint. Target sequence lebih besar dari basis dan
muat pada signed 64-bit; revision registry juga dibatasi untuk storage PostgreSQL/Neo4j.

Pemanggil wajib membaca bytes artefak dengan hash/size terverifikasi dan memeriksa
receipt keputusan registry sebelum membentuk input bertipe. Library tidak membaca
storage atau membuktikan sendiri bahwa sebuah `ArtifactRef` berisi objek yang diberikan.
Entitas/ref registry harus berasal dari view revision yang sah, bukan ID buatan model.
[Registry history](registry-history.md) menyediakan bagian storage untuk binding tersebut;
export artefak view dan wiring worker ASSEMBLE masih harus disambungkan.

Teks normalisasi ber-key ID TextArtifact diperiksa langsung oleh library: SHA-256 dan
panjang harus sama dengan ref normalisasi di DocumentBatch; UTF-8 harus valid; span
start-inclusive/end-exclusive harus berada pada batas code point. Mention surface
harus sama dengan slice teks sumber. Semua dan hanya teks yang direferensikan harus
tersedia. Hash dan UTF-8 diperiksa sekali per teks, kemudian span memakai view `str`.
Kebenaran hubungan blob/regulation/provision-version/chunk mengikuti validator EXTRACT
dan DocumentBatch existing. Metadata source tidak menggantikan review hukum manusia.

## Proses dan output

Source DocumentBatch dan extraction/resolution harus lengkap; partial/deferred
assignment tidak dipublikasikan melalui fungsi ini. Tipe entitas registry harus cocok
dengan kandidat mention dan vocabulary ontology. Registry revision masa depan,
corpus berbeda, ID duplikat, endpoint hilang dan self-edge yang dilarang ditolak.
Materializer mengganti endpoint serta qualifier mention dengan canonical ID, lalu
[canonicalization](graph-canonical-identity.md) mempertahankan qualifier, exception,
temporal scope dan seluruh sumber support sambil melakukan dedup deterministik.

Output memakai GraphDelta existing: entities, mentions, assertions, supports dan
decisions dengan visibility mulai target sequence. Report struktural baru dihitung
dari pemeriksaan aktual; input report tidak disalin sebagai approval. Source input
tidak dimutasi. Urutan output deterministik dan seluruh ID record unik lintas tipe.
Proposal asli tetap dapat dibaca dari artefak RESOLVE yang menjadi dependency.

Dependency menggabungkan EXTRACT/RESOLVE, ref dokumen, ref registry serta hash ontology.
Observasi lookup kosong maupun positif tetap dipertahankan. ID dependency sama dengan
fingerprint berbeda, atau scope sama dengan observasi revision/empty berbeda, menghasilkan
error; tidak memilih salah satu secara diam-diam. Self-dependency pada delta ditolak.

Fungsi ini menghasilkan upsert untuk satu sumber; aliases, profiles, visibility closures
dan support changes kosong. Ketiadaan record tidak berarti perintah menghapus sumber
atau assertion. Jalur incremental memerlukan before-image/dependency closure tersendiri.
Jangan memakai output ini sebagai replacement seluruh corpus. Writer harus memeriksa
base snapshot, registry binding, publication fence, source coverage dan semua receipt
backend sebelum aktivasi. `validation_report.valid` berarti validitas struktural batch,
bukan graph sudah published atau fakta sudah benar secara semantik.
Jika entity/assertion yang sama sudah memiliki visibility pada snapshot lama, writer
harus mempertahankan rentang tersebut atau menggunakan record version yang benar;
target visibility pada delta tidak memberi izin overwrite sejarah record bersama.

## Batas dan verifikasi

Input bytes mencakup protobuf dokumen/extraction/resolution/registry rows/ref/producer
dan teks normalisasi. Limit item/reference serta output wire diterapkan; input ditolak
sebelum cloning hasil jika melebihi batas. Ini bukan janji batas peak RSS tepat sama
dengan bytes wire. Canonicalization memakai map/set terurut; validator EXTRACT existing
masih memeriksa containment terhadap chunk/version spans dan perlu profiling pada
corpus besar. Tidak ada pemanggilan model atau RPC per node di library ini.

Tes memeriksa provenance, hash teks, Unicode, canonical type/revision, dependency
conflict, negative lookup, visibility, ID collision, ordering dan resource limits.
Source fixture bukan corpus/gold nyata. Target latency, throughput, memori dan kualitas
tetap [REQUIRED_UNMEASURED](../configs/benchmark-targets.yaml); aturan kelulusan berada
di [verification.md](verification.md). Worker ASSEMBLE, publication bersama Neo4j,
withdrawal/incremental dan acceptance end-to-end belum diselesaikan oleh library ini.
