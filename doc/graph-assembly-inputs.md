# Input ASSEMBLE dan ekspor registry

Dokumen ini menjelaskan handoff bertipe dari registry PostgreSQL ke builder GraphDelta
Rust. Ia melengkapi [registry history](registry-history.md) dan [GraphDelta](graph-delta.md).
Exporter dan validator tersedia sebagai library; [worker ASSEMBLE](assembly-worker.md)
kini mengeksekusi plan dan mempersist delta. Persiapan artefak/receipt oleh coordinator
dan publication Neo4j masih perlu disambungkan.

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

## Kewajiban integrasi berikutnya

Coordinator memverifikasi receipt keputusan RESOLVE pada revision yang tepat,
menentukan union canonical, mengekspor view dan menyimpannya immutable melalui
FileStore. Logical ID `view.meta.record_id` harus sama dengan `plan.registry_view.artifact_id`;
content address penyimpanan tetap terikat hash/size. Plan harus dipersist dan diikat
ke inventory/checkpoint/job secara durable sebelum dispatch. Worker membaca tepat
bytes keempat role dan teks sumber terverifikasi, lalu menulis delta immutable.
Output artifact dan checkpoint belum boleh dianggap published; Go memegang fencing,
recovery serta receipts backend dan visibility record bersama.

Fixture membuktikan penolakan drift, corruption dan input tidak lengkap, bukan
kebenaran hukum atau model. Ukur SQL/export latency p50/p95/p99, bytes/rows, pool wait,
assembly latency, RSS dan throughput pada [target wajib](../configs/benchmark-targets.yaml).
Status kualitas/performa release tetap **REQUIRED_UNMEASURED**.
