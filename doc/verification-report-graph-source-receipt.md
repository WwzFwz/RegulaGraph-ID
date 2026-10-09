# Verifikasi persistence graph source

Dokumen ini merekam bukti penyimpanan envelope EXTRACT/RESOLVE dan receipt transform
berotoritas. Ini bukan kelulusan K01 menyeluruh atau acceptance Hybrid GraphRAG.

## Revisi, fixture dan lingkungan

Baseline `7cebf3a`; storage receipt pada `80cea85`, workflow/recovery pada `cc6cd90`.
Fingerprint file kode dan fixture disimpan dalam `code-hashes.txt` di
`artifacts/verification/20261009-graph-source-receipt/`, bersama raw log. Run tanggal
2026-10-09 menggunakan Go 1.26.8 windows/amd64, PostgreSQL 16.8 dan Qdrant 1.18.0 lokal.
Migration 0019 dijalankan oleh fixture pada schema terisolasi, bukan deployment produksi.

Fixture `TestPublishedGraphSourceMembershipAgainstStores` menjalankan publication
INDEX dengan PostgreSQL/Qdrant nyata. EXTRACT/RESOLVE kosong dan checkpoint RESOLVE
berhasil disintesis eksplisit; vector/model juga fixture. FileStore memakai direktori
temporer, sedangkan reader input memakai map artefak fixture. Pengujian ini membuktikan
boundary storage/recovery, bukan extraction model nyata atau kebenaran hukum.

## Pemeriksaan dan hasil

Environment backend: `REGULAGRAPH_TEST_POSTGRES_DSN` dan
`REGULAGRAPH_TEST_QDRANT_ENDPOINT` menunjuk instance lokal disposable. Perintah dari root.

| Perintah/pemeriksaan | Expected dan actual | Bukti/exit |
| --- | --- | --- |
| `go test ./src/server/internal/indexing -run '^TestPublishedGraphSourceMembershipAgainstStores$' -count=1 -v` | Membership dan receipt exact diterima; forgeries, cancellation, lease hilang dan target aborted ditolak; PASS | `final.log`, 0 |
| Workflow interruption sebelum receipt commit | Artefak/dependency dapat tersimpan, receipt tidak ada; retry berhasil | Fixture di `final.log`, 0 |
| Acknowledgement hilang sesudah commit | Receipt tetap ada, retry identik berhasil tanpa model | Fixture di `final.log`, 0 |
| Writer menunggu source row lock lalu cancellation di-commit | Menunggu dibuktikan melalui `pg_locks`/`pg_blocking_pids`; receipt ditolak tanpa row sisa | Fixture di `final.log`, 0 |
| Writer menunggu snapshot row lock lalu pin dilepas | Lease hilang terdeteksi sesudah lock dilepas, tidak ada receipt | Fixture di `final.log`, 0 |
| Replay melalui repository baru dengan MaxConnections=1 | Tidak mengambil koneksi pool kedua dalam transaksi, replay berhasil | Fixture di `final.log`, 0 |
| Receipt immutable | Checkpoint berbeda konflik; UPDATE/DELETE ditolak; audit reader tetap tersedia setelah abort | Fixture di `final.log`, 0 |
| `go test ./src/server/... -count=1` | Seluruh package Go lulus dengan backend integration aktif | `go-all.log`, 0 |
| `go vet ./src/server/...` | Tidak ada temuan | `go-vet.log`, 0 |

Regresi menyeluruh dijalankan sebelum penambahan kasus race lease/lost acknowledgement
dan penghapusan satu query lease duplikat pada wrapper read. Bagian indexing terdampak
diuji ulang melalui `final.log`; vet dijalankan sesudah perubahan kode terakhir.
Tidak ada perubahan schema wire, binding generated, Rust atau C++ pada paket ini.

## Temuan dan review manual

Run awal `workflow.log` gagal karena sandbox tidak dapat mengakses compiler cache.
Rerun menemukan `dependency manifest owner mismatch`: payload wire menggunakan logical
batch ID sementara index dependency immutable memakai physical artifact ID. Workflow
kini meng-clone manifest untuk registrasi owner storage; byte dan producer asli tetap
utuh. `workflow-fixed.log`, `concurrency.log` dan `final.log` lulus sesudah perbaikan.
Run yang gagal tidak dihitung PASS.

Review manual memeriksa input bounded/unknown fields, exact transform refs, urutan lock
snapshot/corpus/source, transaksi tunggal untuk membership/checkpoint/registration,
final lease check, canonical JSON/hash serta konsistensi indexed columns. Penulisan
object dilakukan sebelum transaksi receipt; retry deterministik dan orphan tidak
membuat ASSEMBLE job. Pemeriksaan ini menemukan query lease duplikat yang dihapus;
check terakhir tetap ada pada private reader dan sebelum commit writer.

Agent `verify_index_jobs` sempat meninjau authority/provenance dan menyarankan race
tests, kemudian gagal karena batas pemakaian sebelum review diff final selesai.
**Review independen final: NOT_MEASURED/belum terverifikasi.** Tidak ada klaim approval
independen untuk workflow/migration paket ini. Review manual bukan pengganti independensi.

Pembaruan pada kelanjutan berikutnya: reviewer kembali tersedia dan menuntaskan review
receipt serta rerun PostgreSQL/Qdrant, PASS exit 0 di `independent.log` pada direktori
run receipt di atas. Status review independen receipt kini PASS untuk cakupan itu.
Pekerjaan persiapan plan/view setelahnya memiliki [laporan terpisah](verification-report-graph-preparation.md).

## Batas dan pekerjaan berikutnya

Receipt merupakan bukti transform dan authority pada admission, bukan izin publikasi
graph atau pembaruan keputusan registry. Admission job/output harus memeriksa ulang
authority. Registry revision RESOLVE yang lebih kecil dari target belum membuktikan
freshness. Dependency BIND/EXTRACT, historical semantic receipt, revalidasi kandidat
dan reaffirmation lintas revision perlu disambungkan sebelum guard delta diperluas.

Inventory/dispatch ASSEMBLE, writer Neo4j dan publication graph lengkap belum tersedia.
Header/README dan [kontrak coordinator](graph-assembly-coordinator.md) mencatat batas
tersebut. Latency, throughput, memory, kualitas model dan acceptance produksi tetap
**REQUIRED_UNMEASURED** mengikuti [target wajib](../configs/benchmark-targets.yaml).
