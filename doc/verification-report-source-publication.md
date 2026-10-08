# Verifikasi sumber, recovery dan publication INDEX

Dokumen ini mencatat verifikasi paket lanjutan pada 2026-10-09 di atas revision
`194dd69`. Raw log dan fingerprint berada di
`artifacts/verification/20261009-source-binding/`; kontrak penggunaan ada di
[sumber/publication](index-source-publication.md). Schema C01 dan angka benchmark
tidak diubah.

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| Pure source envelope | PASS: record hukum/offset/ID asli tetap, replay stabil, sumber sudah bersnapshot atau scope berbeda ditolak |
| Receipt PostgreSQL | PASS: original checkpoint tetap sah, artefak tanpa receipt ditolak, cancellation/fence/reference/scope drift ditolak, receipt immutable |
| Snapshot staging | PASS: target berbeda ditolak pada kedua urutan bind lalu stage maupun stage lalu bind |
| INDEX processor | PASS: lost commit acknowledgement dikonfirmasi, legacy checkpoint pulih tanpa RPC tambahan, corrupt legacy output menjadi FAILED tanpa klaim ulang |
| Aggregation | PASS: inventory belum selesai tidak mengeluarkan output sukses parsial; bytes corrupt ditolak saat admission ulang |
| PostgreSQL + Qdrant | PASS: schedule/claim/output/recovery/aggregation/write/readback/publication lalu retrieval/hidrasi bukti; late cancellation child/source dan job lock menghalangi activation; receipt graph tidak dapat diganti receipt Qdrant |
| CLI publication | PASS: profil eksplisit, invalid profile tidak dispatch, corpus output diperiksa, deadline dan redaction serta kegagalan output ditangani |
| Semua paket Go | PASS: `go test ./src/server/... -count=1` dengan dua backend lokal; `go-all-final.log` |
| Static analysis | PASS: `go vet` indexing/PostgreSQL/domain/CLI; `vet.log` |
| Model nyata/gold/required benchmark | NOT_MEASURED pada paket ini |

Toolchain Go1.26.8 Windows amd64, PostgreSQL16.8-alpine dan Qdrant1.18.0 disposable.
Fixture memakai kontrak C01 dan vektor/worker sintetis; tidak mengukur akurasi
regulasi atau latency produksi. Unit/fixture PASS bukan acceptance Hybrid GraphRAG.
Tes PostgreSQL indexing memakai schema tersendiri; tidak menyentuh corpus produksi.

`envelope.log` menyimpan kegagalan awal karena dependency issuer tidak ikut
dipertahankan; diperbaiki dan lulus `envelope-fixed.log`. `completion.log`,
`recovery-corrupt.log`, `publication.log`, `publish-command.log` dan `cli-final.log`
menyimpan run terfokus berikutnya. Perubahan wrapper publication/CLI setelah
broad suite diuji kembali pada paket terkait; broad suite tersebut bukan bukti
pengujian kode yang ditambahkan sesudahnya.

Reviewer independen `/root/verify_index_jobs` memeriksa source binding,
aggregation/recovery dan boundary publication. Temuan target snapshot yang
berbeda, inference ulang saat recovery, error integritas yang terus retry dan
late cancellation sudah diperbaiki serta diuji ulang. Run independen terakhir
paket tersebut PASS pada `independent-final.log`. Wrapper operator publication
yang ditambahkan kemudian mendapat review tambahan PASS; log terpisah
`independent-publish-command.log` memuat tes PostgreSQL/Qdrant dan CLI terbaru.

Pekerjaan tersisa mencakup persiapan inventory melalui CLI dari CHUNK nyata,
run native/corpus penuh, graph/review-resume, answering produksi, incremental,
observability/operasi dan gold/acceptance. Deployment tetap di luar scope.

## Persiapan snapshot operator

Lanjutan di atas revision `6504676` menambahkan `PrepareInitialSourceSnapshot`
dan CLI `prepare-snapshot`. `artifacts/verification/20261009-snapshot-preparation/`
menyimpan targeted.log, packages.log, vet.log serta independent.log, semuanya
PASS. Perintah: `go test ./src/server/internal/indexing ./src/server/cmd/cli
-count=1` dengan PostgreSQL/Qdrant disposable, dan `go vet` pada kedua paket.
Reviewer `/root/verify_index_jobs` memeriksa boundary ini secara independen.

Fixture memeriksa facts terhitung, exact manifest hash, receipt sumber, replay,
perubahan generation/scope, duplikasi dan bytes corrupt. Export C01 diperiksa
dengan validator produksi dan existing file tidak dapat ditimpa. Schema facts
sesuai loader evaluator; jumlah graph nol tidak dianggap quality/eligibility
PASS. Seluruh CLI belum dijalankan terhadap corpus PDF pengguna; hasil ini
membuktikan library/adapter/export, bukan benchmark produksi. Registrasi
statistik dan penjadwalan generation melalui operator masih perlu dilanjutkan.
