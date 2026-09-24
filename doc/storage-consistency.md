# Storage, identitas, snapshot, dan recovery

Dokumen ini merancang persistensi dan konsistensi lintas PostgreSQL, Qdrant, Neo4j, serta storage artefak. Perannya menetapkan cara data baru menjadi terlihat tanpa mencampur snapshot atau kehilangan provenance. Fondasi control-plane S01 sudah aktif untuk job durable, request/checkpoint recovery, operation ledger, receipt, active-snapshot CAS, pin pembaca, dan artefak lokal immutable. Visibility record di Qdrant/Neo4j, mutasi/kompensasi backend nyata, dependency closure lengkap, retention/GC, backup/restore, serta fault injection lintas proses tetap spesifikasi untuk X01/U01/O01 pada [rencana pengembangan](development-plan.md).

## 1. Identitas dan fingerprint

Regulation, provision, serta canonical entity memakai ID registry opaque yang stabil. Natural identity keys seperti tipe/issuer/jurisdiksi/nomor/tahun membantu lookup tetapi bukan nama tampilan yang bisa berubah. Bila natural key belum lengkap, record provisional disimpan dan tidak di-merge hanya berdasarkan kemiripan teks. Merge/split dicatat sebagai keputusan revisioned; snapshot lama tetap memetakan mention sesuai keputusan saat itu.

SourceBlob diidentifikasi hash bytes asli. SourceObservation terpisah sehingga blob identik dari BPK dan JDIHN tetap memiliki dua jalur provenance. TextArtifact, Chunk, SupportRecord, serta artefak turunan memakai content/dependency fingerprint. Identitas ketentuan dan identitas versinya tidak sama. Perubahan satu ayat mempertahankan identitas bagian yang tidak berubah sambil menghasilkan versi baru pada bagian terdampak.

Fingerprint memakai encoding kanonik versioned: field bertipe dan length-prefixed, map/set diurutkan menurut key yang ditentukan schema, list semantik mempertahankan urutan. Jangan hash serialisasi JSON/Protobuf arbitrer dan menganggap seluruh runtime akan menghasilkan bytes identik. Field waktu observasi/run ID yang bukan input semantik dikeluarkan dari fingerprint hasil turunan tetapi tetap tersimpan dalam audit. Fixture lintas bahasa harus membuktikan encoding, Unicode, absent/empty, dan collision handling.

Assertion fingerprint mencakup canonical endpoint, predicate, qualifier/exception, temporal semantics, dan ontology version. Support identity mencakup assertion + exact evidence version/span + producer fingerprint. Duplicate mirror tidak menambah confidence karena independent_source_group sama. Record payload immutable setelah diterbitkan; metadata visibility boleh ditutup oleh publication berikutnya tanpa mengubah payload historis.

## 2. Model waktu

Ada dua sumbu utama: legal applicability (tanggal aturan berlaku) dan knowledge visibility (snapshot yang mengetahui fakta itu). Observation/fetch time merupakan audit akuisisi. Masing-masing disimpan terpisah. Pertanyaan tahun lampau dapat memakai pengetahuan terbaru atau snapshot historis yang diminta; API harus membedakan dua pilihan tersebut.

Tanggal berlaku unknown, status revoked, superseded sebagian, conflict, serta tidak berlaku adalah status berbeda. Interval legal menggunakan [start, end) hanya bila diketahui; absence tanggal tidak otomatis berarti berlaku selamanya. Perubahan parsial menyimpan affected provision path dan support. Interpretasi konflik tidak hanya ditentukan dokumen paling baru: simpan alternatif/evidence dan gunakan review bila resolver tidak punya dasar yang jelas.

## 3. Pemilik storage dan tabel logis

Go adalah pemilik mutasi state persisten. Rust menghasilkan batch immutable; C++ hanya melayani inference; Python membaca endpoint/artefak. File hasil batch boleh ditulis worker ke staging storage yang diizinkan, tetapi manifest publication dan registry tetap dimiliki Go.

| Storage | Kelompok record | Kunci/index/constraint yang dirancang |
| --- | --- | --- |
| PostgreSQL | corpus, source_observation, source_blob, regulation, document_edition | PK opaque; unique blob hash; index corpus/identity key; observation tidak ditimpa |
| PostgreSQL | text_artifact, structure_node, provision, provision_version, legal_change_event, chunk | FK source/version/parent; unique derived fingerprint; hierarchy cycle check; temporal state eksplisit |
| PostgreSQL | canonical_registry_revision, identity_key, mention_assignment, alias, resolution_decision | Unique key dalam scope bila identitas disahkan; expected revision; history merge/split |
| PostgreSQL | assertion/support archive, dependency_edge, lookup_scope_revision, review_item | Arsip untuk replay/rebuild Neo4j; reverse dependency index; review actor/reason |
| PostgreSQL | jobs, attempts, leases, checkpoints, operation_ledger, outbox | Unique idempotency key + payload hash; fencing token; durable operation status |
| PostgreSQL | snapshots, publication_manifest, backend_receipts, active_snapshot, read_leases | Unique corpus/sequence; single active pointer; CAS parent; per-reader bounded lease |
| Qdrant | Dense and sparse records + visibility/temporal payload | Compatible representation generation; payload indexes corpus/seq/type/temporal; deterministic point mapping |
| Neo4j | Entity/provision revisions, assertion nodes, support nodes, typed adjacency | Unique application record key; version visibility on node/edge/support; directed predicate semantics |
| Blob storage | Original PDF/HTML, extracted text/mapping, model output, batches, manifests | Content-addressed immutable objects, hash/size checks; temporary objects promoted after completion |

Archive relasi pada PostgreSQL dapat berupa manifest dan immutable batch refs disertai metadata yang cukup untuk rebuild, tidak harus menggandakan seluruh isi graph sebagai tabel OLTP terdenormalisasi. Pemilihan fisik diuji throughput/recovery; sumber replay harus tetap tersedia. Mapping opaque record ID ke point ID backend menggunakan registry yang menyimpan full ID dan memeriksa collision, bukan memotong hash tanpa pemeriksaan.

## 4. Aturan visibility lintas backend

Untuk snapshot sequence S, record terlihat jika corpus cocok, from_seq <= S, dan to_seq tidak diisi atau S < to_seq. Query juga memeriksa representation generation, policy tipe dokumen, dan legal date. Node/edge/support Neo4j yang dipakai dalam path semuanya harus terlihat; cukup memfilter node awal saja tidak memadai.

Snapshot sequence menyatakan revisi corpus, bukan salinan penuh semua record. Record unchanged tetap terlihat pada interval yang mencakup snapshot baru. Update metadata yang memengaruhi jawaban/filters menghasilkan revision baru; record lama tidak di-upsert isinya pada ID yang sama.

Qdrant mendukung filter payload; implementasi adapter akan menyusun filter visibility tersebut dan menguji operator serta tipe payload pada versi server yang dipin. Filter setelah top-k saja dapat menurunkan recall, sehingga filter corpus/visibility/temporal yang dapat diekspresikan harus masuk retrieval backend. [Dokumentasi filtering Qdrant](https://qdrant.tech/documentation/search/filtering/)

## 5. Publication serial per corpus

1. Coordinator memperoleh hak publisher tunggal untuk corpus dan fencing epoch persisten. Reservasi target sequence T, parent snapshot S, operation ledger, serta manifest dicatat sebelum mutasi backend.
2. Perhitungan batch dapat paralel di luar publication lock. Sebelum stage, base snapshot/registry revision diverifikasi; hasil stale direbase atau dihitung ulang pada dependency terdampak.
3. Artefak immutable disimpan beserta checksum. Backend upsert record baru dengan from_seq=T. Record superseded ditutup dengan to_seq=T; query lama S<T tetap melihat payload lama. Semua operasi memiliki deterministic operation key, before-image visibility, expected revision, dan hash batch.
4. Tunggu acknowledgement dan validasi setiap backend. Periksa counts, ID, sumber/support, endpoint, filter visibility, serta kesiapan indeks melalui jalur read yang akan digunakan. Write accepted atau enqueued saja belum cukup. Replica/read routing harus memiliki watermark yang memenuhi manifest.
5. Snapshot menjadi READY hanya setelah semua receipt sesuai. Dalam transaksi PostgreSQL, CAS active_snapshot dari S ke T, tandai COMMITTED, dan simpan audit/outbox notification. Titik ini adalah publication logis bagi request baru.
6. Reader baru mengambil T; reader lama memegang S. Cache active pointer diinformasikan lewat notification tetapi source of truth tetap PG; cache stale tidak boleh membuat request mengklaim snapshot berbeda dari yang dipakai.

State snapshot: PREPARING -> STAGING -> VALIDATING -> READY -> COMMITTED. Kegagalan sebelum commit menuju RECOVERING atau ABORTING -> ABORTED; COMMITTED tidak dibatalkan dengan mengedit history.

Protokol ini tidak mengandalkan transaksi atomik PostgreSQL/Qdrant/Neo4j. Tidak boleh ada dua publisher efektif untuk corpus. Leader takeover harus memagari writer lama dan memastikan seluruh write in-flight miliknya sudah selesai/ditolak sebelum kompensasi dan publisher berikutnya berjalan. Lease PG saja tidak memagari write Neo4j/Qdrant yang sudah dikirim. Adapter memakai command dispatcher tunggal yang memvalidasi epoch, akses tulis terbatas ke dispatcher, dan operation ledger. Jika keheningan writer lama tidak dapat dibuktikan, publication dihentikan sementara; query pada snapshot committed tetap berjalan. Safety lebih penting daripada mengklaim HA yang belum dapat dibuktikan.

## 6. Abort, recovery, dan rollback

Jika proses mati setelah Qdrant berhasil tetapi Neo4j gagal, pointer aktif masih S. Recovery membaca ledger dan melanjutkan operasi idempotent pada target T yang sama. Ia tidak membuat T+1 dan melompati staging yang belum dibereskan.

Jika T harus dibatalkan, hapus/retire record stage T dan kembalikan closure to_seq lama menurut before-image. Tunggu semua mutasi in-flight lama selesai sebelum kompensasi; verifikasi setiap backend. Publisher berikutnya tidak boleh memakai sequence lebih tinggi hingga abort selesai, sebab closure yang tertinggal dapat membuat record lama hilang pada snapshot masa depan. Ini wajib menjadi fault-injection test.

Crash sesudah CAS aktif tetapi sebelum job SUCCEEDED dipulihkan dengan membaca snapshot COMMITTED dan menyelesaikan status job/outbox. Jangan mempublikasikan ulang atau melakukan kompensasi snapshot yang sudah committed. Operation ledger dapat direplay tanpa mengekstraksi ulang sumber/model.

Rollback logis dari snapshot buruk dibuat sebagai publication baru yang mereferensikan kembali state baik melalui revision/visibility baru. Pointer tidak diputar mundur sambil meninggalkan visibility masa depan tanpa rekonsiliasi. Backup restore boleh memilih manifest committed lama di lingkungan restore terisolasi, lalu diverifikasi lintas backend sebelum menerima trafik.

## 7. Reader lease dan garbage collection

Request mem-pin snapshot serta generation sepanjang retrieval, parent hydration, streaming, dan terminal validation. Read lease dibatasi deadline; disconnect/cancel melepasnya. Long evaluation run memperoleh pin eksplisit. Garbage collector hanya menghapus data di luar retention policy yang tidak direferensikan snapshot retained, read lease, job aktif, atau artefak evaluasi yang wajib direproduksi.

Retensi angka operasional belum ditentukan; konfigurasi wajib menyatakannya sebelum produksi. Implementasi saat ini tidak melakukan penghapusan fisik otomatis. Backup meliputi manifest PG, registry, model/config refs, immutable blobs, serta kemampuan restore/rebuild indeks yang sesuai. Restore drill harus membuktikan citation sumber, temporal query, counts, dan invariant, bukan hanya server database dapat start.

## 8. BM25, generation, dan statistik corpus

Snapshot filtering saja tidak selalu mengisolasi statistik scoring sparse. Dokumentasi Qdrant menyebut IDF modifier tidak otomatis terisolasi hanya dengan payload partition. Karena itu statistik scoring adalah bagian manifest indeks, bukan detail mutable yang diabaikan. [Dokumentasi Qdrant tentang IDF isolation](https://qdrant.tech/documentation/manage-data/multitenancy/)

Rancangan utama memakai BM25 sparse dengan analyzer, term dictionary, k1/b, N, df, dan average document length yang dibekukan per lexical statistics generation. Rust menyiapkan sparse document weights; Go memakai analyzer/dictionary identik untuk query weights. Built-in dynamic IDF modifier dinonaktifkan pada jalur ini. Skor dihitung terhadap statistics generation yang disebutkan manifest, bukan diasumsikan selalu statistik corpus terkini.

Vocabulary berupa registry ID term yang append-only; penambahan term tidak mengganti ID lama. Normalisasi/tokenisasi lintas Go/Rust memiliki golden fixtures. Term baru yang tidak ada dalam statistik generation memakai df=0 dengan formula smoothing versioned dan N generation tersebut. Corpus kosong tidak menghasilkan generation yang siap query. Drift dan unseen-term ratio dicatat; refresh statistik membentuk generation baru serta reweight/reindex seluruh record yang bergantung padanya.

Keuntungan desain ini: update dokumen kecil tidak diam-diam mengubah scoring semua snapshot lama. Tradeoff: statistik dapat tertinggal sampai refresh, dan refresh memiliki biaya corpus-wide. Quality gates tetap berlaku; bila drift merusak kualitas, perbaiki refresh/implementasi atau pilih isolasi generation fisik yang terukur. Tidak boleh mengklaim BM25 statistik live sekaligus snapshot scoring immutable tanpa mekanisme isolasi.

Pergantian embedding model/dimensi, analyzer, ontology incompatible, atau lexical stats membuat representation generation baru. Bangun indeks side-by-side, verifikasi completeness/parity, lalu publish manifest baru. Snapshot lama merujuk generation lama hingga retensi aman. Full rebuild pembanding incremental memakai manifest/model/statistics yang sama; rebuild dengan statistik baru adalah eksperimen berbeda.

## 9. Dependency invalidation

| Perubahan | Closure yang perlu dinilai ulang |
| --- | --- |
| Bytes PDF/HTML berubah | Parse, mapping, struktur, chunk dan seluruh turunan terdampak |
| Metadata tanggal/status berubah | Temporal revisions, filters, assertion applicability, jawaban cache terkait |
| Parser/chunker berubah | Artefak yang bergantung pada versi tersebut; raw blob dapat dipakai ulang |
| Embedding/tokenizer berubah | Index generation; extraction tidak otomatis diulang bila dependency tidak berubah |
| Prompt/model ekstraksi berubah | Extraction/support, resolution bila terpengaruh, graph/profile/index turunannya |
| Canonical merge/split | Mention assignment, assertion endpoint, alias lookup, profiles, seed links |
| Dokumen baru memberi definisi/rujukan | Consumer yang sebelumnya unresolved atau tidak menemukan kandidat dalam scope itu |
| Satu mirror hilang | Observation availability; tidak otomatis mencabut regulasi atau menghapus support independen |
| Source dikeluarkan operator | Tutup membership/support terkait; pertahankan assertion yang masih punya support sah |

Dependency mencatat input langsung serta lookup scope/revision. Dependency negatif penting: pencarian definisi yang tadinya kosong dapat berubah akibat dokumen baru. Exact artifact fingerprint saja tidak menangkap kasus tersebut. Impact planning menggunakan reverse edges dan scope invalidation; traversal closure mencapai fixed point dan mendeteksi cycle pemrosesan.

Source+dependency identik memakai artefak yang sama tanpa panggilan model ulang. Replay hasil semantik menggunakan respons yang tersimpan. Job baru karena dependency berubah tidak disebut unchanged re-extraction. Rebuild-equivalence membandingkan canonical assignment, versi, support, membership, dan index input logis, dengan timestamp/run ID nonsemantik dinormalisasi menurut prosedur tertulis sebelum tes.

## 10. Matriks kegagalan wajib

| Titik kegagalan | Hasil yang harus dibuktikan |
| --- | --- |
| Unduhan putus/checksum salah | Blob tidak dipromosikan; observation/job gagal terlapor |
| Worker crash setelah checkpoint | Lease habis, retry dari checkpoint; tidak ada duplicate publication |
| Dua job key sama, payload beda | CONFLICT; hasil job lama tidak tertimpa |
| Canonical registry berubah saat worker bekerja | Expected-revision check menolak delta stale |
| Satu backend gagal saat stage | Active pointer tetap parent; reader tidak melihat stage baru |
| Crash setelah closure ditulis | Recovery/abort mengembalikan konsistensi sebelum publication berikutnya |
| Writer lama hidup setelah takeover | Fencing/drain mencegah stale write; bila tidak bisa, publication ditahan |
| Crash setelah pointer commit | Job/outbox dipulihkan dari manifest tanpa kompensasi data committed |
| Query lama saat publish/GC | Evidence, parent, dan graph path tetap dari snapshot yang dipin |
| Satu support dihapus | Assertion tetap jika ada support sah lain; tidak ada orphan |
| Snapshot/model generation berbeda | Request ditolak atau diarahkan ke generation cocok, tidak dicampur |
| Restore storage tidak serasi | Readiness gagal; manifest mismatch tidak diluluskan |

Setiap skenario harus mempunyai fixture dan expected state, lalu diuji terhadap adapter nyata sebelum release. Benchmark yang berkaitan mencakup UPDATE, GRAPH.COMMIT_THROUGHPUT, ISOLATION, dan seluruh INVARIANT pada YAML; protokol konsistensi tidak menjadi alasan mengubah target secara sepihak.

## 11. Locator bukti untuk resolution

Katalog `extraction_evidence_sources` pada migration 0011 mengikat mention ID, artefak, checkpoint EXTRACT sukses, corpus, fingerprint snapshot, dan auth scope. `SaveExtractionCheckpoint` menulis checkpoint serta seluruh locator dalam satu transaksi dengan lease/fence/cancellation check. Recovery mempertahankan locator checkpoint sukses pertama; duplikat hanya diterima jika set mention dan scope identik. Batch partial/failed tidak dikatalogkan. Tabel append-only ini bukan publication marker atau bukti identitas canonical.

Lookup kandidat mengambil seluruh support yang diminta dalam satu query berbatas; support hilang, lintas scope, atau overflow tidak berubah menjadi sukses parsial. Workflow membaca ulang bytes berhash, document/text closure, dan alias support sebelum model menerima excerpt. Reuse lintas snapshot memerlukan membership proof tersendiri dan belum tersedia. Artefak sebelum migration perlu revalidasi; tidak ada backfill yang mengasumsikan input lama sah. Input/output model menyimpan dependency unik ke seluruh artefak pendukung untuk invalidation dan replay.
