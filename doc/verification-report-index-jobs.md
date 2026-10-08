# Verifikasi inventory dan claim INDEX

Dokumen ini mencatat pembuktian boundary inventory/job INDEX pada 2026-10-09,
di atas revision `dbd3270`. Fingerprint file, log dan hasil mentah berada di
`artifacts/verification/20261009-index-jobs/`. Lingkungan Go 1.26.8 Windows amd64,
PostgreSQL 16.8 disposable; tidak ada deployment atau perubahan benchmark.

Tes `TestInitialIndexInventoryRestoresAdmission` dan
`TestInitialIndexJobInventoryAgainstPostgres` lulus: reconstruct admission,
ownership copy, plan hilang/berubah, authority hilang, scheduling atomik,
rollback assignment terakhir, exact replay, drift auth scope, pool satu koneksi,
generic claim exclusion, cancellation sumber, lease expiry, stale owner,
retry exhaustion, terminal checkpoint recovery dan publisher fence stale.
Fixture memakai dokumen Rust-derived serta request/checkpoint sintetis.
Checkpoint terminal pada tes claim hanya menguji scheduling; bukan bukti output
embedding benar atau checkpoint INDEX produksi sudah terintegrasi.

Verifier terpisah `verify_index_jobs` meninjau diff dan menjalankan ulang tes
fokus PostgreSQL: PASS, tidak menemukan blocker konkret. Log independen terakhir
`independent-recovery.log`. Batasnya mencakup belum adanya executor durable
lengkap dan keharusan memeriksa authority kembali setelah claim.

Run awal `initial.log` gagal karena fixture meminta retry delay nol; fixture
diperbaiki memakai delay positif sesuai kontrak. Run `go-all.log` menemukan
fixture dictionary berbagi schema dengan suite adapter yang melakukan reset;
fixture kini mendapat schema tersendiri. `go-all-isolated.log` dan
`independent-packages.log` gagal ketika dua proses suite PostgreSQL berjalan
bersamaan dan mereset schema publik; keduanya disimpan sebagai FAIL, bukan PASS.
Run pengganti eksklusif `go test ./src/server/... -count=1` PASS (exit 0),
dicatat terpisah pada `go-all-exclusive.log`. Opt-in Qdrant/native tidak diaktifkan
pada run ini; skip bukan bukti integrasi backend tersebut.

Target latency/throughput, kualitas model, corpus besar, dan acceptance Hybrid
GraphRAG: NOT_MEASURED. Daemon INDEX, checkpoint/output commit serta publication
dari seluruh child durable masih pekerjaan integrasi berikutnya. Fixture PASS
tidak menutup milestone X01 secara keseluruhan.
