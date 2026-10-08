# Identitas assertion dan support prapublikasi

Dokumen ini menetapkan normalisasi identitas graph setelah resolution untuk library Rust
`knowledge_graph/assembly/canonical.rs`. Tujuannya menggabungkan relasi yang sama dalam
satu view terverifikasi sambil mempertahankan seluruh dukungan sumber. Ini belum
GraphDelta, penyimpanan Neo4j, withdrawal transaction atau kelulusan K01 penuh.

## Input dan output

`assemble_canonical_relations` menerima EXTRACT, RESOLVE, ref sumber, ontology,
canonical ID set pada registry revision terverifikasi, serta budget item/byte.
Ia memakai materializer endpoint existing sebelum canonicalization. Caller Go harus
membuktikan hash sumber/receipt registry; Rust tidak mengautentikasi DB melalui ID set.
`canonicalize_relations` merupakan primitive setelah materialisasi tersebut, dengan
prasyarat endpoint/ontology/receipt sudah diperiksa caller.

Output memiliki assertion/support/mention dan dua mapping: ID assertion lama ke baru,
serta ID support lama ke baru. Setiap input tetap terhitung meskipun record identik
digabungkan. Output belum memiliki visibility publication. Record input ber-visibility
ditolak agar data snapshot published tidak diam-diam ditulis ulang.

## Hash versi 1

ID assertion berbentuk `assertion:canonical:v1:<sha256>`, ID support berbentuk
`support:canonical:v1:<sha256>`. Digest menggunakan domain prefix, byte NUL, dan
protobuf payload yang dinormalisasi dengan record ID dikosongkan. Corpus/schema
tetap berada di payload. Tidak ada map protobuf pada record yang didukung; unknown
fields ditolak secara rekursif agar evolusi schema tidak menghasilkan hashing
nondeterministik atau membuang informasi yang mungkin penting. Perubahan kebijakan
hash atau schema yang memengaruhi identitas membutuhkan review/migrasi versi.

Assertion mempertahankan subject/predicate/object berarah, qualifier, exception refs,
origin explicit/inferred, ontology dan **seluruh temporal scope**. Dengan demikian
knowledge snapshot yang berbeda tetap dibedakan secara konservatif; ID ini bukan
klaim bahwa dua fakta lintas snapshot/corpus telah dibuktikan sama secara hukum.
Unknown effective date tidak diubah menjadi tanggal atau interval rekaan.

Qualifier diurutkan berdasarkan encoding dan duplicate identik digabung; numeric -0
dinormalisasi menjadi +0. Compare dates diurutkan/dedup dengan validasi C01 ulang;
normalisasi yang membuat COMPARE tidak valid ditolak. Exception refs diubah ke ID
canonical target terlebih dahulu, lalu diurutkan/dedup. Topological order menjaga
perubahan exception mengubah ID parent. Missing target dan cycle ditolak eksplisit,
bukan dihilangkan. Dukungan cycle memerlukan rancangan identitas closure tersendiri.

Support memakai assertion ID hasil, source version refs dan spans yang diurutkan/dedup,
extraction producer, independent-source group dan review state. Source berbeda tetap
record berbeda. Mirror group dipertahankan; banyak support bukan otomatis banyak bukti
independen. Producer dan field lainnya dipertahankan persis: ini bukan inference
kesetaraan model atau penyatuan producer manifest yang hanya tampak mirip.

Semua record hasil diurutkan berdasarkan ID; mention tetap memakai identitas aslinya.
Collision lintas mention/assertion/support serta canonical entity ditolak pada boundary
yang memiliki ID tersebut. Collision hash dengan payload berbeda juga ditolak.

## Integrasi dan batas

Budget input dicek sebelum clone; record/referensi dan serialized input/output dibatasi,
termasuk pertumbuhan panjang ID hasil. Batas byte adalah ukuran payload, bukan janji
peak RSS: mapping, indeks dan temporary sorting memakai memori tambahan yang berbatas
input/item. Ukur RSS serta assembly throughput/p95/p99 pada workload nyata mengikuti
[target required](../configs/benchmark-targets.yaml), yang tetap REQUIRED_UNMEASURED.

Pengujian memastikan renaming ID ekstraksi/reordering tidak mengubah hasil, qualifier
dan arah berbeda tidak tergabung, dua sumber tetap terlacak, exception remap stabil,
dan menghilangkan satu support dari input mempertahankan ID relasi serta support lain.
Kasus terakhir adalah recomputation library; belum membuktikan withdrawal durable di
Neo4j atau full incremental equivalence. [Laporan](verification-report-graph-canonical.md)
mencatat scope verifikasi. Langkah berikutnya adalah GraphDelta + dependency/closure,
snapshot registry view, assembly worker dan publication Go/Neo4j.
