# Inventory dan penjadwalan ASSEMBLE

Dokumen ini menjelaskan admission sumber, penyimpanan inventory dan claim job graph.
Go/PostgreSQL memiliki authority penjadwalan; Rust tetap menghasilkan GraphDelta.
Library ini belum mengeksekusi worker, meng-commit output atau memublikasikan Neo4j.

## Input dan admission

`GraphJobInventory` berisi assignment child job, source job, GraphAssemblyPlan C01
dan reference plan. Seluruh assignment diurutkan berdasarkan source job, memiliki
ID unik, serta berbagi corpus, publication/fence, base snapshot, target sequence,
registry revision, scope, ontology dan producer. Satu plan mencakup satu sumber
lengkap. ID artefak milik plan tidak boleh bertabrakan lintas assignment.
Validator domain memeriksa bentuk dan hash plan; ia tidak memberi izin scheduling.

`PrepareGraphJobAdmission` menerima inventory dan bytes asli/bound DocumentBatch,
EXTRACT, RESOLVE, candidate batch bila nonempty, serta RegistryEntityView tiap sumber.
Ia mengautentikasi registration/hash/size, menghitung ulang envelope source, memeriksa
BIND historical identity, dependency EXTRACT, receipt/intent/ledger RESOLVE, seluruh
candidate lookup positif/negatif, canonical selection dan exact registry view.
Ontology dan config tetap terikat. DEFER dan action yang belum didukung ditolak.
Revision RESOLVE harus sama dengan revision plan; reaffirmation lintas revision belum
tersedia dan tidak diganti dengan pengubahan artefak historis.

Preflight mengambil current registry revision/history floor sebelum membaca evidence.
Hasilnya opaque `GraphJobAdmission` yang memiliki salinan plan dan stamp tersebut.
Pemanggil tidak dapat memberi boolean approval atau mengganti pointer plan setelah
verifikasi. Reads/hash/decode mahal dilakukan sebelum transaksi scheduling; tidak ada
pool acquisition bersarang ketika transaksi memegang koneksi. Pool satu koneksi diuji.

## Commit inventory dan replay

`admission.Schedule(ctx, pin)` mengunci target snapshot, corpus, kemudian source jobs
terurut. Stamp current revision/floor wajib tetap sama dengan preflight. Perubahan
apa pun meminta preflight ulang, termasuk perubahan tak terkait; ini bukan permintaan
otomatis untuk inference ulang. Target historical revision pada plan tetap tidak berubah.
Writer registry harus menjaga stamp; namespace alias `lookup:` dilarang melalui
`AdvanceLookupScope` generik dan hanya boleh ditulis writer alias versioned.

Di bawah lock, Schedule memeriksa publication fence/base/registry binding, latest
checkpoint dan cancellation sumber, membership published base, reference plan
terdaftar serta live reader pin. Inventory dan semua child jobs QUEUED/ASSEMBLE
ditulis satu transaksi. Gagal pada child terakhir pun tidak menyisakan inventory
parsial. Exact replay tetap memeriksa authority, kemudian tidak membuat job duplikat.
`LoadGraphJobInventory` memeriksa hash, bentuk, kolom dan canonical payload untuk
audit; hasilnya bukan admission atau izin menjalankan worker.

Migration `0020_graph_job_inventory.up.sql` wajib diterapkan sebelum binary dengan
generic claim baru, karena query claim merujuk tabel assignment graph. Tabel inventory
dan assignment append-only, ber-FK ke source binding, jobs dan artefak. Tidak ada
backfill; abort mempertahankan audit. GC/retirement memerlukan lifecycle terpisah.

## Claim dan batas resource

`ClaimGraphJob` memilih child dengan SKIP LOCKED, lease/fence dan retry budget.
Publication/base harus masih aktif, source tidak dibatalkan, source checkpoint cocok,
serta registry binding masih retained. Generic `ClaimJob` mengecualikan child graph.
Reclaim setelah lease kedaluwarsa menaikkan fence; claim tidak membuktikan freshness
semua evidence dan bukan pengganti recheck saat dispatch/output commit.

Inventory maksimal 256 assignment; aggregate source evidence dan payload inventory
masing-masing dibatasi 64 MiB. Worker per sumber tetap maksimal 16 MiB termasuk plan,
empat role dan normalized text yang diperlukan. Batas byte wire bukan janji peak RSS.
Ukur preflight read/hash, pool/lock wait, retry, queue p95/p99, throughput dan memory
pada [benchmark required](../configs/benchmark-targets.yaml); belum diukur pada workload
release. Tidak ada inference atau RPC per node pada admission ini.

## Kelanjutan

Sambungkan pembacaan inventory dan re-admission per job ke worker Rust, verifikasi
GraphDelta terhadap source/plan, lalu simpan checkpoint dan STAGED atomik dengan
recovery lost acknowledgement. Setelah itu lengkapi writer/readback Neo4j dan publication
semua backend. Fixture integration inventory saat ini memakai EXTRACT/RESOLVE kosong;
uji source nonempty lengkap, multi-document lintas revision dan benchmark tetap terbuka.
