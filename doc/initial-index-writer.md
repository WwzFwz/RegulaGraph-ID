# Writer indeks snapshot awal

Dokumen ini menjelaskan library Go yang menerima hasil INDEX terverifikasi,
menulis dense/BM25 ke Qdrant, dan menghasilkan receipt untuk publication.
Cakupannya satu snapshot awal dengan daftar sumber yang dinyatakan pemanggil;
ini belum wiring ingestion daemon atau bukti Hybrid GraphRAG siap digunakan.

## Kontrak pemanggilan

`PlanInitialIndex` sekarang menyiapkan `InitialIndexPlans` dari daftar job CHUNK
yang dipilih operator tepercaya, target snapshot, generation, producer, auth scope
dan ukuran batch 1–128 chunk. Ia membaca artefak terdaftar, membuktikan checkpoint
CHUNK, snapshot/scope identik, closure dokumen dan otoritas dictionary. Setiap sumber
wajib nonempty/COMPLETE; sumber/job/chunk duplikat ditolak. Urutan sumber dan chunk
stabil, seluruh chunk dibagi tepat sekali, maksimal 256 plan. Ini bukan discovery
otomatis atau bukti bahwa daftar pilihan mencakup semua regulasi di corpus.

Statistik harus memiliki snapshot, input policy dan jumlah dokumen yang sama dengan
seluruh chunk terpilih. Fingerprint populasi dan nilai DF tidak dihitung ulang oleh
planner; producer statistik tetap wajib menghitungnya dari rendered input yang tepat.
Planner belum menyediakan producer statistik corpus tersebut.

Plan/record ID memakai SHA-256 domain terpisah dan field berpanjang u64 big-endian.
Plan ID mengikat publication/fence, source job serta byte protobuf deterministik plan
sebelum ID plan/output diisi. Identitas ini berlaku untuk versi producer Go ini,
bukan janji canonical serialization lintas bahasa. Reference plan memakai record ID
typed dan storage key Rust `sha256/aa/bb/<sha256>.bin`; hash mengikat byte final.

`Persist` menulis semua plan secara immutable, mendaftarkan metadata dan dependency
source/analyzer/statistics/dictionary. Retry identik memperbaiki registrasi yang
terputus. Caller hanya menjadwalkan setelah seluruh pemanggilan sukses. `Batches`
mengembalikan salinan; perubahan input atau salinan tidak mengubah inventory privat.

`WorkerRequest` menerima claim INDEX aktif dari scheduler dan menuntut request
context dengan scope/snapshot/config yang tepat, membatasi deadline terhadap lease.
Satu child job durable harus memiliki satu plan karena worker menyimpan satu request
per job/attempt/fence. `ExecuteBatch` memverifikasi ulang plan terdaftar, checkpoint
sumber dan lexical authority sebelum RPC; output harus sukses, hanya INDEX, dan
checkpoint/producer/attempt/fence serta context batch harus identik. Byte output
diperiksa hash, ukuran, schema dan `ValidatePlannedIndexBatch` sebelum diserahkan
sebagai `VerifiedIndexOutput`. `Register` menyimpan reference/dependency output;
caller tetap wajib commit checkpoint dengan fence dan mengelola recovery/cancel.
Library tidak menganggap object `JobRecord` sebagai autentikasi API publik.

`PrepareOutputs` menuntut satu batch untuk setiap plan yang disetujui. Jumlah sama
dengan plan duplikat, reference plan diganti, atau plan lain yang secara lokal valid
tetap ditolak. Setelah itu admission writer lama memeriksa ulang seluruh coverage.
Cache output dipakai bersama agar tidak membaca byte embedding dua kali. Batas
64 MiB adalah jumlah serialized artefacts; decoded protobuf menambah pemakaian RAM.

Coordinator lebih dahulu membekukan daftar job sumber yang diizinkan dan reserve
publication. Ia menyediakan `IndexCatalogBinding` dari konfigurasi tepercaya,
bukan input query: publication/fence, generation, origin Qdrant dan collection.
Collection khusus satu publication tidak boleh dipakai oleh writer eksternal,
alias endpoint berbeda, atau deployment lain dengan katalog PostgreSQL terpisah.
Keunikan SQL berlaku pada pasangan literal endpoint/collection, bukan resolusi DNS.

`PrepareInitialIndex` membaca batch, plan, source dan artefak lexical melalui
registry corpus dan byte SHA-256/size. Ia memanggil gate plan/source produksi,
memeriksa CHUNK checkpoint terbaru untuk job tersebut, serta mapping dictionary
pada revisi PostgreSQL. Checkpoint CHUNK gagal/unknown terbaru ditolak; checkpoint
EXTRACT berikutnya tidak membatalkan keberhasilan CHUNK. Context source dan plan
harus menunjuk snapshot target yang sama. Preparation membuktikan seluruh chunk
setiap sumber **yang dinyatakan**, bukan seluruh PDF unduhan atau seluruh job DB.
Otorisasi pengguna dan pemilihan inventory tetap milik coordinator pemanggil.

`ArtifactRef.artifact_id` untuk `DocumentBatch` dan `IndexBatch` keluaran Rust
dapat berupa `artifact:<namespace>:<hash>`, berbeda dari `meta.record_id` logis.
Preparation memeriksa media type pesan yang tepat serta ref/hash/size terdaftar;
ID logis batch INDEX harus tetap sama dengan `plan.output_batch_id`. Sumber
diikat transitif oleh byte plan dan checkpoint CHUNK. Identitas typed untuk plan,
analyzer, dictionary dan statistik tetap wajib persis, bukan dilonggarkan untuk
seluruh artefak. Cache dan budget byte tetap berdasarkan alamat fisik terverifikasi.

Semua batch memakai generation, target dan ancestry dictionary identik. ID
record/chunk tidak boleh duplikat. Statistik frozen harus cocok policy,
snapshot populasi dan jumlah dokumen; sparse term harus berada dalam dictionary.
Ini memverifikasi artefak hasil producer dan tidak menghitung ulang BM25 atau
membuktikan kualitas embedding. Byte unik dibatasi total 64 MiB, setiap artefak
16 MiB, input 256 batch. Batas ini adalah kapasitas library awal, bukan perubahan
workload atau kelulusan benchmark. Corpus lebih besar memerlukan manifest paginated
dan streaming verification sebelum jalur tersebut tersedia.

Objek `PreparedInitialIndex` memiliki state privat. `ExpectedBackend()` memberi
checksum/count untuk dimasukkan ke manifest oleh coordinator, bersama seluruh
backend yang wajib untuk profil aplikasi. Commit write-set v1 memakai domain
`regulagraph-initial-index-write-v1` diikuti byte NUL, lalu string UTF-8 dengan
panjang u64 big-endian: publication ID, fence desimal, endpoint, collection,
corpus ID, generation ID; kemudian job ID, batch artifact ID dan SHA-256 untuk
setiap input yang diurutkan berdasarkan artifact ID. Referensi plan/source/lexical
terikat transitif oleh byte batch. Ini bukan hash semantik protobuf.

## Penulisan dan kegagalan

`WriteInitialIndex` memeriksa backend fisik dan manifest staged terhadap prepared
set. Parent snapshot dan closure ditolak. Advisory session lock per publication
menserialisasi bootstrap/dispatch; pool harus menyisakan koneksi untuk transaksi
metadata di samping koneksi lock. Pelepasan lock idempotent, dan koneksi dibuang
jika unlock gagal. Koleksi/record immutable membuat late replay berisi data sama;
protokol ini tidak membenarkan late delete atau closure.

Migration 0014 menyimpan generation immutable, target fisik, payload record dan
UUID point. ID point v1 memakai SHA-256 domain `regulagraph-index-point-v1` + NUL
dan tiga string length-prefixed u64 big-endian: corpus, generation, record ID.
16 byte pertama menjadi UUID versi 8/variant RFC, sedangkan digest penuh disimpan
dan diperiksa bersama pemilik/payload. Collision atau retry dengan isi berbeda
ditolak, termasuk rollback seluruh page SQL. Catalog tidak mengaktifkan route query.

Intent operasi durable dicatat sebelum I/O Qdrant. Dispatch/readback memakai page
maksimal 64 record dan 512 KiB protobuf. Seluruh expected point dibaca ulang
beserta payload dan kedua vector. Pemeriksaan serving menyegarkan layout/index,
menuntut satu shard/satu replica/write consistency satu yang sehat, memeriksa
jumlah point exact, lalu menjalankan query dense dan sparse berfilter produksi.
Count mengikuti [API resmi Qdrant 1.18](https://api.qdrant.tech/v-1-18-x/api-reference/points/count-points).
Probe tersebut bukti route berfungsi, bukan Recall@k atau kualitas hukum.

Kegagalan meninggalkan intent planned/applied untuk retry identik. Tidak ada
auto-abort, delete, atau klaim compensated tanpa penghapusan nyata. Bootstrap
collection yang gagal di tengah pemasangan index tetap memerlukan recovery
eksplisit sesuai adapter. Retry write sesudah publication terminal ditolak;
retry `Publish` yang sudah committed tetap idempotent tanpa mengaktifkan ulang
snapshot historis. Commit memeriksa publisher fence di bawah lock corpus.

## Batas penerbitan dan pekerjaan berikutnya

Receipt ini hanya untuk Qdrant. Production coordinator wajib menetapkan backend
yang diperlukan (termasuk graph/metadata sesuai profil), admission source inventory,
job cancellation/lease, dan query hydration sebelum mengaktifkan aplikasi. Library
publication lama memeriksa semua backend yang tercantum; ia tidak menentukan profil
aplikasi. Tes publication Qdrant saja memakai corpus terisolasi. Tes kedua menuntut
Neo4j dan membuktikan publication diblokir tanpa receipt-nya, tanpa memalsukan ack.

Belum tersedia: persistent inventory/child-job scheduler INDEX, producer statistik
corpus dari rendered input, incremental parent/closures, copy-forward parent points, recovery
bootstrap, retire/GC, topology terdistribusi, writer Neo4j, coordinator INDEX daemon,
dan alur PDF nyata sampai jawaban. Reader katalog/hydration kini tersedia sebagai
[library terpin](pinned-evidence.md), dengan uji draft memakai model sintetis. Preparation belum
boleh dipromosikan menjadi endpoint publik yang menerima daftar sumber sembarang.

Ukur p95/p99 preparation/lock/batch/readback, throughput, pool occupancy, RSS,
freshness dan konsistensi snapshot. Required gates tetap
[benchmark-targets.yaml](../configs/benchmark-targets.yaml), **REQUIRED_UNMEASURED**.
Tes integrasi dan keterbatasannya dicatat di [laporan verifikasi](verification-report-initial-index.md).
