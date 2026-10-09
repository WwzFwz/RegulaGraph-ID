# Input ASSEMBLE dan ekspor registry

Dokumen ini menjelaskan handoff bertipe dari registry PostgreSQL ke builder GraphDelta
Rust. Ia melengkapi [registry history](registry-history.md) dan [GraphDelta](graph-delta.md).
Exporter dan validator tersedia sebagai library; [worker ASSEMBLE](assembly-worker.md)
kini mengeksekusi plan dan mempersist delta. Library coordinator mempersist plan/view
dari receipt sumber; inventory/dispatch durable dan publication Neo4j masih perlu disambungkan.

## Seleksi canonical pada revisi terikat

`Repository.ExportRegistryEntityView` menerima corpus, publication, fence, revision,
ID artefak logical, producer dan daftar canonical ID dengan batas item/byte eksplisit.
Satu transaksi read-only RepeatableRead memeriksa binding publication immutable,
history floor, snapshot state dan profil pada revision tersebut. Hasil `RegistryEntityView`
menyimpan semua dan hanya ID yang diminta, dalam urutan ID menaik, beserta entity rows
yang diperiksa hash, kolom indeks, tipe, scope, review state dan exact identity-nya.
Profil hilang, duplikat, rusak atau tidak cocok menggagalkan seluruh ekspor. Seleksi
kosong diperbolehkan secara eksplisit; ia tidak berarti seluruh registry kosong.

Revision tiap entity adalah revision profil yang dipilih dan tidak boleh melampaui
revision view. Query SQL membatasi jumlah payload sebelum transfer; wire validation
memeriksa ukuran akhir. Ini membatasi bytes input/output, bukan menjanjikan peak RSS
sama dengan ukuran protobuf. Ekspor tidak mengubah registry atau menyetujui keputusan
model. Exporter tidak menebak label/type dari canonical ID.

STAGING/VALIDATING memerlukan publisher fence yang sama pada snapshot transaksi;
PUBLISHED dapat dibaca secara historis setelah publisher berikutnya bergerak.
RepeatableRead menjamin konsistensi data yang dibaca, bukan lease/fence tetap aktif
sampai fungsi selesai. Coordinator wajib memeriksa ulang authority/checkpoint/registry
receipt saat admission dan commit. View hasil transaksi tidak merupakan token otorisasi.

## Plan, role dan konteks

`GraphAssemblyPlan` mematok DocumentBatch, ExtractionBatch, ResolutionBatch dan
RegistryEntityView melalui empat ArtifactRef berbeda, source checkpoint RESOLVE,
publication/fence, base snapshot, target sequence, revision registry, hash ontology,
producer dan ID delta keluaran. Target harus lebih besar dari base dan muat pada
integer signed 64-bit. Setiap artefak maksimal 16 MiB, total role maksimal 64 MiB;
library delta juga menerapkan budget gabungan protobuf dan teks normalisasi.
`ProcessBatchRequest.graph_assembly_plan` menyediakan slot referensi plan di kontrak;
handler Rust mengonsumsi referensi tersebut sesuai [kontrak worker](assembly-worker.md).

Go `domain/graph_assembly.go` memeriksa shape, schema, exact coverage dan binding view.
Rust `assembly/inputs.rs` memeriksa aturan yang sama, ontology aktual, source refs,
snapshot/auth scope/config, serta exact union canonical ID dari keputusan RESOLVE.
Batch resolusi dibatasi sebelum membuat himpunan ID. Setelah lolos, wrapper memanggil
builder GraphDelta existing yang memverifikasi dokumen, teks, assignment dan supports.
Konsumen stage menolak unknown fields secara rekursif; transport wire generik tetap
mempertahankannya untuk kompatibilitas. Baseline C01 tidak ditulis ulang.

Plan/view memakai media type protobuf dengan nama message lengkap. DocumentBatch
menerima media vendor dan alias typed yang sudah dipakai produksi. EXTRACT menerima
media vendor atau media protobuf generik legacy; RESOLVE menerima generik legacy
atau typed ResolutionBatch. Decode wajib memilih message sesuai role dan memverifikasi
schema, hash, byte size serta isi, bukan mempercayai MIME generik sebagai autentikasi.
Tidak ada schema JSON paralel.

## Persiapan coordinator yang tersedia

`workflows.PrepareGraphAssembly` menerima corpus/publication/source-job, pin base,
producer, hash ontology dan batas reference/candidate. Ia membaca receipt graph source
yang immutable, membuktikan membership, lalu memakai `ReadGraphResolutionReceipt`
untuk membaca keputusan asli. Untuk mention nonempty, reader mengambil intent dan
kandidat terdaftar serta receipt transaksi historis, merekonstruksi ResolutionBatch
dengan builder produksi, lalu menuntut kesamaan seluruh payload. Empty extraction
tidak membuat atau membutuhkan operasi registry fiktif. Tahap ini mengautentikasi
keputusan masa lalu, bukan freshness pada revision lain.

Artefak bound CHUNK/EXTRACT/RESOLVE dibaca ulang dengan hash/size/registration exact.
Closure sumber dan ontology hash pada EXTRACT producer diperiksa. Recorded registry
revision wajib sama dengan revision target; mismatch menghasilkan `ErrResolutionReplan`.
Sesuai kemampuan worker saat ini, seluruh keputusan harus LINK/CREATE dengan satu
canonical assignment. DEFER/REJECT/MERGE/SPLIT ditahan melalui `ErrGraphAssemblyUnresolved`
sebelum ekspor/write, bukan menghilangkan mention atau menganggap graph lengkap.
Penerimaan action CREATE di builder tidak menambah kemampuan writer RESOLVE otomatis.

Union canonical dipilih deterministik, lalu diekspor melalui registry revision-bound.
View dan plan memiliki logical ID dari seed protobuf deterministik yang mengikat target,
source checkpoint/refs, producer dan ontology. Storage key keduanya content-addressed;
dependency index mengikat input dan hash ontology, dengan view menjadi dependency plan.
Retry identik memakai bytes dan ID yang sama. Tidak ada model call selama preparation.

Reader keputusan membatasi EXTRACT/RESOLVE/kandidat gabungan 16 MiB. Budget worker
terpisah membatasi CHUNK/EXTRACT/RESOLVE bound, view, plan dan normalized text yang
dibutuhkan hingga 16 MiB; teks dengan logical ID yang sama dihitung sekali. MIME
normalized text mengikuti worker persis `text/plain;charset=utf-8`. Ini batas byte
wire, bukan janji peak RSS; normalized text diperiksa bytes/provenance oleh worker.

Authority publication diperiksa sebelum write dan setelah persistence, bersama source
checkpoint serta live membership/pin pada pemeriksaan akhir. Crash/cancellation dapat
menyisakan immutable orphan, tetapi tidak membuat job. `PreparedGraphAssembly` hanya
locator artefak; hasil ini **bukan** capability admission atau bukti freshness dependency.

## Kewajiban integrasi berikutnya

Coordinator kini memverifikasi receipt keputusan RESOLVE, menentukan union canonical,
mengekspor view dan menyimpannya immutable melalui FileStore. Logical ID
`view.meta.record_id` harus sama dengan `plan.registry_view.artifact_id`;
content address penyimpanan tetap terikat hash/size. Plan harus dipersist dan diikat
ke inventory/checkpoint/job secara durable sebelum dispatch. Tahap admission tersebut
masih harus membuktikan dependency freshness BIND/EXTRACT dan keputusan lintas revision,
bukan hanya exact recorded revision. Worker membaca tepat
bytes keempat role dan teks sumber terverifikasi, lalu menulis delta immutable.
Output artifact dan checkpoint belum boleh dianggap published; Go memegang fencing,
recovery serta receipts backend dan visibility record bersama.

Fixture membuktikan penolakan drift, corruption dan input tidak lengkap, bukan
kebenaran hukum atau model. Ukur SQL/export latency p50/p95/p99, bytes/rows, pool wait,
assembly latency, RSS dan throughput pada [target wajib](../configs/benchmark-targets.yaml).
Status kualitas/performa release tetap **REQUIRED_UNMEASURED**.
