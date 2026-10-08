# INDEX build plan dan handoff worker

Dokumen ini menetapkan kontrak perakitan `IndexBatch` dari chunk dan artefak lexical
terverifikasi. Worker Rust menghasilkan artefak immutable; Go tetap memiliki
otoritas registry, snapshot, ledger backend, dan publikasi. Ini bukan bukti bahwa
X01 atau RAG end-to-end sudah selesai.

## Input dan proses

`ProcessBatchRequest` untuk satu stage INDEX wajib membawa `index_build_plan` dan
tepat satu `sources` yang sama dengan `IndexBuildPlan.document_batch`. Plan berisi
source snapshot, target snapshot, generation, rantai dictionary root sampai target,
pilihan pasangan chunk/record ID, closure, output batch ID, producer, dan policy lexical.
Manifest request harus identik dengan producer plan. Model generation harus identik
dengan model runtime; versi atau hash berbeda ditolak sebelum native RPC.

`indexing/build.rs` membaca byte plan, DocumentBatch, analyzer, dictionary dan statistik
dengan hash/ukuran/storage key terverifikasi. ID typed analyzer/dictionary/statistik/plan
adalah identitas logis; store Rust memakai identitas fisik `artifact:<namespace>:<hash>`.
Adapter pembacaan typed menerjemahkan identitas fisik saja, menjaga hash, storage key,
media type dan ukuran. Byte plan mengikat semua referensi secara transitif. Coordinator
tetap perlu mendaftarkan dependency transitif tersebut untuk invalidasi incremental.

Batch sumber harus COMPLETE dan cocok dengan source snapshot. Target tidak boleh
lebih tua, dan sequence sama menuntut seluruh SnapshotRef sama. Rantai dictionary
mempertahankan seluruh mapping parent dan harus memuat exact frozen-statistics base.
Populasi statistik tidak boleh lebih baru daripada target. Otoritas lineage dan
membership snapshot lintas revisi tetap perlu diverifikasi Go sebelum publication.

Dense dan BM25 memakai teks `structure-labels-v1` yang sama: label induk/pemilik
dan span utama terverifikasi. `LexicalStatisticsArtifact.input_policy` wajib cocok;
ekspor token-only lama dengan field kosong tetap dapat dibaca, tetapi tidak dapat
dipakai INDEX sampai producer membuktikan dan mencatat policy populasi. Jangan
mengisi field itu pada statistik yang dihitung memakai teks berbeda.

Worker menyiapkan sparse lebih dahulu, memanggil embedding native satu batch, lalu
menggabungkan hasil berdasarkan chunk ID. Partial result, item asing/hilang, duplikat,
truncation, drift model, norm tidak valid, OOV dokumen, dan source version yang
ditolak/quarantine menyebabkan kegagalan seluruh pilihan. Zero-term chunk ditolak
secara eksplisit; tidak dihilangkan dari accounting. Statistik tetap dapat menghitung
zero-term documents dalam denominator populasi sesuai kontrak lexical.

Filter berpasangan diproyeksikan dari versi/pasal/regulasi/source yang sama dengan
provenance embedding. Record baru ber-visibility `[target.sequence, infinity)`.
Visibility input tertutup tidak dapat mendukung record terbuka. Tidak adanya
visibility bukan bukti membership; Go harus memeriksanya. Closure hanya diteruskan
sesuai plan, tanpa worker mengubah record lama.

## Output dan checksum

`IndexBatch` memuat generation, records dense+sparse, closures, counts, dependency
plan, serta reference plan. `expected=accepted=jumlah upsert`, rejected nol; closure
dihitung terpisah dalam operation bound. Producer dari coordinator adalah pin pekerjaan,
bukan attestation binary worker. Artefak ditulis setelah seluruh hasil valid; cancellation
setelah write dapat menyisakan objek immutable tanpa checkpoint sukses, bukan publikasi.

Checksum v1 adalah SHA-256 atas urutan berikut, bukan hash serialisasi protobuf:

1. Byte domain `regulagraph-index-plan-v1` diikuti satu byte NUL.
2. String hex SHA-256 byte plan, didahului panjang byte UTF-8 u64 little endian.
3. Jumlah records u64 little endian, lalu records dalam urutan plan.
4. Untuk tiap record: record ID dan chunk ID sebagai string berpanjang seperti langkah 2;
   jumlah dense values u64, diikuti bit IEEE754 float32 little endian setiap value;
   jumlah sparse entries u64, diikuti pasangan ID u32 little endian dan bit float32.

Byte hash plan mengikat input, model, generation, closures, dan producer. Checksum
sendiri **tidak** membuktikan proyeksi filter/dependency. Go wajib menjalankan
`ValidatePlannedIndexBatch` terhadap plan dan source yang byte-nya sudah diverifikasi:
gate memeriksa selection/order, seluruh source filters, metadata, visibility, dependency,
closure, counts, unit norm serta checksum. Setelah itu, katalog/writer tetap memerlukan
registry authority, prior-state closure, dictionary term membership, fence dan receipt.

## Batas dan bootstrap

Satu plan maksimal 128 upserts, 128 closures, dan 64 dictionary revisions. Rendering
maksimal 1 MiB/item dan 2 MiB/pilihan. Native memiliki batas pesan sendiri 4 MiB dan
menolak token overflow. Batch output memakai wire limit 16 MiB/1.000.000 items agar
128 embedding 4096 dimensi tidak tersandung batas 100.000 item pesan umum. Pembacaan
artefak/normalisasi sinkron tidak dapat dihentikan di tengah; batas render bukan batas RSS.
Plan, source DocumentBatch, dan setiap artefak lexical dibatasi 16 MiB, dengan
100.000 wire items untuk source/plan/lexical. Total ukuran analyzer, statistik dan
seluruh rantai dictionary maksimal 64 MiB per plan, diperiksa sebelum I/O. Corpus
besar harus dipartisi menjadi batch dengan seluruh dependency lokal tetap lengkap;
partitioning otomatis belum terhubung dan batas ini tidak mengubah workload release.
Belum ada cache generation lintas plan; ukur biaya sebelum memilih strategi cache.

Bootstrap worker memakai tiga variabel opsional yang harus lengkap bersama:

- `REGULAGRAPH_WORKER_NATIVE_ENDPOINT`, contoh `http://127.0.0.1:50054`.
- `REGULAGRAPH_WORKER_EMBED_MANIFEST`, file protobuf C01 `ModelManifest`, maksimal 64 KiB.
- `REGULAGRAPH_WORKER_EMBED_MANIFEST_SHA256`, hash exact byte file tersebut.

Manifest ini bukan bundle JSON export model. Channel native dan handle Tokio dipasang
sekali; eksekusi sinkron worker berada pada blocking pool. Native memvalidasi pin model
setiap request. Tanpa konfigurasi native, INDEX gagal `FailedPrecondition`; tahap lain
tetap tersedia. Library Go kini membentuk/persist plan dari inventory CHUNK terverifikasi
dan menyediakan dispatch dengan admission output. Producer statistik corpus, persistent
inventory/child-job scheduling, dispatch durable coordinator,
wiring backend/publication dan antarmuka query RAG masih pekerjaan berikutnya.
Library [writer snapshot awal](initial-index-writer.md) sudah tersedia terpisah:
hash/checkpoint admission, katalog PostgreSQL, Qdrant dan receipt telah diuji
dengan backend nyata; daemon INDEX belum memanggil library ini secara otomatis.

## Verifikasi

Tes CHUNK→INDEX memakai text artifact nyata dan dense fixture, lalu mengekspor protobuf
melalui `REGULAGRAPH_INDEX_FIXTURE_DIR`. Go `TestRustIndexBuildAdmission` membaca output
Rust yang sama dan menguji mutasi vektor, filter, snapshot, producer dan dependency.
Tes tanpa variabel tersebut SKIP, bukan bukti interop. Tidak ada klaim kualitas model,
gold, latency/throughput referensi, atau publication dari fixture. Target tetap
[benchmark-targets.yaml](../configs/benchmark-targets.yaml), REQUIRED_UNMEASURED.
