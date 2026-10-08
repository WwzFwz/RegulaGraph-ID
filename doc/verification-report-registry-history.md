# Verifikasi registry history dan binding publication

Laporan ini mencatat verifikasi primitive PostgreSQL untuk revisi registry terpin,
snapshot lama, migration floor dan lease pembaca. Cakupan berupa library storage;
hasil tidak menyatakan integrasi graph request, K01 penuh, atau kualitas model lulus.

Tanggal: 2026-10-09. Baseline `76ea0e3`; fingerprint file final dan raw log berada di
`artifacts/verification/20261009-registry-history/results.json`. Toolchain Go 1.26.8
windows/amd64, PostgreSQL 16.8 disposable pada port 55448. Broad Go suite juga memakai
Qdrant disposable port 56348. Fixture sintetis membuat identitas/alias dan publication
PostgreSQL, bukan persetujuan corpus hukum pengguna. Migration upgrade diuji pada
schema terisolasi dari 0017 ke 0018, lalu schema fixture dibersihkan.

## Pemeriksaan dan hasil

| Pemeriksaan | Perintah / bukti | Hasil |
| --- | --- | --- |
| Alias latest tetap kompatibel | `go test ./src/server/internal/adapters/postgres -run 'TestRegistry(Aliases\|SnapshotHistory)' -count=1 -v`, `targeted.log` | PASS, exit 0 |
| Suite adapter sebelum tambahan kasus race/migrasi | `go test ./src/server/internal/adapters/postgres -count=1`, `postgres.log` | PASS, exit 0 |
| Historical snapshot dan upgrade | `go test ./src/server/internal/adapters/postgres -run 'TestRegistry(SnapshotHistory\|HistoryMigration)' -count=1 -v`, `history.log` | PASS, exit 0 |
| Review/rerun independen | `/root/verify_index_jobs`, `independent.log` | PASS, exit 0; empat top-level tests |
| Regression Go seluruh modul | `go test ./src/server/... -count=1`, `go-all.log` | PASS, exit 0; tes bersyarat mengikuti env yang tersedia |
| Static check adapter | `go vet ./src/server/internal/adapters/postgres`, `vet.log` | PASS, exit 0 |
| Latency/throughput/quality release | Suite required produksi/gold belum dijalankan | NOT_MEASURED |

Awal percobaan build sandbox gagal mengakses cache Go; rerun dengan akses toolchain
yang disetujui berhasil. Ini keterbatasan lingkungan, bukan PASS dari percobaan gagal.

Expected/actual: snapshot lama mempertahankan satu alias pada scope lama dan hasil
kosong pada scope baru; setelah registry maju, snapshot baru melihat dua alias pada
scope lama dan satu alias baru. Hasil sama sesudah repository dibuka ulang. Fence,
revision, owner, sequence dan expiry palsu ditolak. Exact binding replay tidak berubah;
UPDATE/DELETE history/binding ditolak. Count cap tidak melakukan truncation.

Upgrade mempertahankan baseline count yang diketahui, menolak revision sebelum floor,
dan menolak count legacy NULL. Writer lookup generik tidak dapat mengubah scope alias
tanpa versioned registry write. Ini menjaga keterlacakan hasil kosong/positif.

## Temuan dan batas

Temuan MVCC lease **CLOSED**: repeatable-read membuat final check tidak melihat lease
yang dilepas saat query masih berjalan. Jalur pinned kini read-committed dengan
revision immutable. Regression menahan lookup menggunakan table lock PostgreSQL,
melepas lease saat query terblokir, lalu memastikan hasil ditolak setelah lock dibuka.
Reviewer independen mengonfirmasi perbaikan dan tidak menemukan blocker terbuka
pada cakupan library ini.

Binding masih opt-in, tanpa GraphDelta validation/publication atau entity linker
runtime. Corpus existing tidak mendapat rekonstruksi history sebelum upgrade;
snapshot lama tanpa binding tetap tidak eligible untuk lookup pinned ini. Tidak ada
pengujian merge/split canonical, benchmark beban referensi, atau klaim seluruh graph
siap. Follow-up mengikuti [kontrak](registry-history.md) dan [rencana K01](k01-implementation-plan.md).
