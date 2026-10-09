# Penyimpanan generation graph Neo4j

Dokumen ini menjelaskan kontrak adapter Neo4j untuk menyimpan GraphDelta yang sudah
melewati admission coordinator, lalu membuktikan kesesuaian isi backend sebelum
generation dapat diusulkan untuk publication. Ia melengkapi [graph delta](graph-delta.md)
dan [konsistensi storage](storage-consistency.md); status SEALED di Neo4j belum
mengubah snapshot aktif PostgreSQL.

## Pemilik dan alur

Rust menghasilkan GraphDelta C01; domain/workflow Go memeriksa source, registry,
canonical projection, provenance dan dependency. PostgreSQL menyimpan checkpoint
STAGED. Adapter [Neo4j](../src/server/internal/adapters/neo4j/README.md) hanya memiliki
penulisan dan verifikasi backend, bukan keputusan resolution atau publication.

| Tahap | Input | Output dan invariant |
| --- | --- | --- |
| `New` | Config Bolt, credentials, database, pool/timeout dan Binding | Driver reusable tanpa koneksi saat konstruksi; Binding mengikat corpus, generation, publication/fence, base snapshot, target sequence dan revision registry. |
| `EnsureSchema` | Context berdeadline | Tiga uniqueness constraints untuk generation, record dan operation; definisi existing harus sama. Penulisan sebelum schema siap ditolak. |
| `ApplyGraphDelta` | Delta additive yang telah di-admit | Satu transaksi node, edge, operation receipt dan penghitung budget; acknowledgement bukan izin serving. |
| `VerifyAndSeal` | Inventory delta lengkap untuk generation | Proof berisi binding hash, operation-set hash dan jumlah record/edge/operation; hanya diberikan setelah exact verification dan seal atomik. |
| Publication coordinator berikutnya | Proof ditambah authority dan inventory durable | Belum tersambung: catalog graph, receipt PostgreSQL dan snapshot manifest harus terikat sebelum pointer aktif berpindah. |

Adapter memakai driver `github.com/neo4j/neo4j-go-driver/v5` versi 5.28.4 dengan
transaksi eksplisit. Pola transaksi dan query berparameter mengikuti
[dokumentasi driver](https://neo4j.com/docs/go-manual/current/transactions/);
tes memakai Neo4j Community 5.26.0. Versi ini dipin, bukan klaim versi terbaru.

## Identitas, projection dan replay

Setiap record memakai key `(corpus, generation, id)` dan menyimpan payload protobuf
deterministik beserta hash serta properti pencarian bertipe. Entity, mention,
assertion, support dan decision tidak digabung menjadi satu identitas. Assertion
memiliki edge subject/object; support menunjuk assertion sehingga dua sumber yang
mendukung relasi sama tetap menyimpan dua support. Qualifier, exception dan assigned
entity memakai jenis edge tertutup. Endpoint harus ada dan bertipe benar; seluruh
record memakai visibility target sequence tanpa closure.

Operation ID dan hash seluruh delta membedakan exact replay dari isi yang berubah.
Replay tidak menambah penghitung budget. Generation lock mengurutkan writer dan
verifier; konflik apa pun membatalkan seluruh transaksi delta, termasuk record yang
telanjur ditulis dalam halaman sebelumnya. Sesudah SEALED, exact replay masih boleh,
tetapi operation baru ditolak. Binding berbeda, termasuk fence baru, tidak dapat
mengambil alih generation lama. Coordinator berikutnya perlu menentukan recovery
atau generation baru dari authority PostgreSQL, bukan mengubah binding di tempat.

Verifikasi membandingkan semua properti, payload, arah/jenis edge, coverage operation
dan penghitung generation. Missing/extra records, duplicate edges, incoming edges
dari namespace atau label yang salah, serta payload/properti yang rusak ditolak.
Perbandingan properti berjalan di Neo4j dan hanya scalar hasil pemeriksaan dikirim
balik; nilai corrupt yang besar tidak dikirim ke klien sebagai payload readback.

## Resource, kegagalan dan batas integrasi

Satu delta mengikuti batas wire 16 MiB. Satu generation dibatasi 256 operation dan
64 MiB total input protobuf, termasuk record bersama yang dikirim kembali oleh
delta berbeda. Writer memeriksa budget kumulatif di bawah lock yang sama dengan
commit; verifier memakai batas yang sama. `UNWIND` dibagi per 256 record/edge.
Ini batas intake saat ini, bukan hasil benchmark atau batas peak RSS: projection,
map, property arrays dan transaksi backend juga memakai memori. Sebelum workload
lebih besar diterima, desain inventory/verification bertahap harus tetap menjaga
coverage lengkap, bukan memotong data diam-diam.

Pool eksplisit 1–128 koneksi dan timeout positif maksimal lima menit. Deadline
caller tetap berlaku; driver tidak melakukan retry transaksi tersembunyi. Lost
acknowledgement dapat direkonsiliasi melalui exact replay dan verifikasi ulang.
URI hanya direct `bolt`/`bolt+s`; transport plaintext hanya menerima alamat IP
loopback. Tidak ada klaim readiness replica/cluster dari pemeriksaan satu backend.

Alias/profile records, visibility closures dan support changes ditolak pada tahap
ini. Generation historis tidak dihapus oleh writer; cleanup pada tes hanya menyentuh
corpus/generation milik fixture. Adapter tidak menyediakan HTTP/CLI, traversal query,
validasi semantik hukum, maupun transaksi atomik lintas Neo4j/PostgreSQL/Qdrant.
Bootstrap dan coordinator merupakan caller terpercaya; koneksi yang memiliki izin
menulis langsung ke database berada di luar proteksi lock aplikasi.

## Verifikasi dan pekerjaan berikutnya

[Laporan verifikasi](verification-report-neo4j.md) mencatat backend nyata, replay
bersamaan, rollback, shared support, corruption, budget dan output Rust aktual.
[Catalog/write-intent](graph-generation-catalog.md) kini tersedia. Selanjutnya receipt graph, carry-forward indeks
snapshot dasar yang sah, publication/recovery, lalu traversal dan evidence hydration.
Proof tidak boleh diubah menjadi receipt dari input caller tanpa recheck authority.

Ukur commit/lock/queue latency p50/p95/p99, throughput, fan-out, verification time,
pool saturation dan peak RSS pada volume serta concurrency yang dinyatakan. Gate
angka tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml), berstatus
REQUIRED_UNMEASURED untuk milestone ini; fixture correctness bukan acceptance.
