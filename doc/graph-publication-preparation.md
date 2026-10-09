# Persiapan inventory graph untuk publication

Dokumen ini menjelaskan pengumpulan output ASSEMBLE committed dan validasi ulang
seluruh graph sebelum writer Neo4j/publication mengonsumsinya. Ia menghubungkan
[eksekusi ASSEMBLE](graph-job-execution.md) dengan [graph store](neo4j-graph-store.md).
Hasil prepared adalah paket input terverifikasi; belum receipt durable atau
perpindahan snapshot aktif.

## Input, proses dan output

`GraphJobAdmission.ReadCompletedGraph(ctx, pin)` menerima admission opaque yang
sebelumnya memeriksa sumber, keputusan RESOLVE, candidate freshness dan registry
view. Satu transaksi mengunci target publication, corpus registry, seluruh source
dan child job, lalu reader pin. Stamp revision/history-floor, binding publication,
base snapshot, inventory hash/count/fence, source checkpoint dan membership sumber
harus tetap sama. Perubahan registry memerlukan admission baru; perubahan tak
terkait dapat diterima setelah preflight memeriksanya, tanpa mengubah history.

Semua child wajib STAGED dan tidak dibatalkan. Checkpoint harus memiliki ID, corpus,
job, fence, stage, producer dan terminal status yang benar; hash payload database
diperiksa. Source cancellation juga menolak pembacaan. Hasil memiliki urutan yang
sama dengan assignment inventory, dengan checkpoint dan artifact ref lengkap.
Job belum selesai menghasilkan `ErrGraphOutputsPending`, bukan hasil sukses parsial.
Error ini tidak memicu model/Rust ulang secara otomatis; state durable tetap menjadi
dasar recovery atau tindakan operator.

`PrepareCompletedGraph` kemudian membaca plan, empat role sumber, normalized text
yang diperlukan dan GraphDelta dengan hash/size check. Helper sumber yang sama dipakai
oleh `ExecuteGraphAssembly`; `ValidatePlannedGraphDelta` memeriksa canonical projection,
provenance dan bukti teks. Workflow tidak memanggil model atau worker. Sesudah I/O,
authority dibaca ulang dan seluruh plan/checkpoint/output dibandingkan exact.
Checkpoint baru setelah recovery membuat hasil prepared lama tidak berlaku meskipun
bytes delta tetap sama; preparation baru dapat memakai checkpoint yang baru.

`PreparedGraphOutputs.Completed()` dan `Deltas()` menghasilkan salinan agar caller
tidak mengubah hasil admission. `Revalidate` memeriksa authority pada saat dipanggil.
Ia bukan lock yang bertahan selama remote write; publisher tetap wajib mengulang
authority di transaksi yang menyimpan receipt dan mengaktifkan snapshot.

## Batas dan integrasi

Inventory mengikuti batas 256 assignment. SQL menahan transfer checkpoint individual
di 16 MiB dan gabungan di 64 MiB; metadata checkpoint/ref serta output yang dideklarasikan
juga dibatasi. Workflow membaca paling banyak 64 MiB sumber secara gabungan dan
64 MiB output, dengan batas sumber per assignment 16 MiB. Nilai tersebut membatasi
input serialized, bukan peak RSS; salinan dan decoded messages perlu diprofilkan.
Rows ditutup sebelum lookup artefak di transaksi yang sama sehingga pool satu
koneksi tetap bekerja. Context mengikuti deadline reader pin dan caller.

Identitas output fisik dapat berbeda dari logical `GraphDelta.meta.record_id`.
Checkpoint menunjuk artefak fisik; validator delta mengikat logical ID ke plan.
Output tidak boleh memakai ID role input atau output child lain. Canonical entity
yang sama boleh muncul pada delta sumber berbeda; konsistensi isi lintas delta
tetap diperiksa writer/seal generation, bukan disamakan dengan kepemilikan artefak.

[Catalog/write intent immutable](graph-generation-catalog.md) dan pemeriksaan gabungan
record lintas delta sebelum remote mutation kini tersedia. Berikutnya receipt backend authoritative serta
publication graph bersama indeks snapshot dasar. Prepared inventory tidak boleh
langsung digunakan untuk memindahkan active pointer atau mengabaikan backend lain.
Carry-forward indeks harus mempertahankan generation, source membership, statistik
BM25 dan model manifest yang sama; snapshot target tetap memerlukan admission reader
yang benar sebelum graph traversal/hydration.

[Laporan verifikasi](verification-report-graph-completed.md) membedakan PostgreSQL
nyata, actual Rust bytes, RPC dan batas fixture. Ukur collection/lock/read/hash p95/p99,
pool wait dan RSS pada workload [benchmark required](../configs/benchmark-targets.yaml).
Quality dan performance acceptance tetap NOT_MEASURED.
