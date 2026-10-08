# Verifikasi planner dan boundary dispatch INDEX

Dokumen ini mencatat pembuktian library Go untuk inventory CHUNK terpilih,
persistence plan, dispatch worker dan admission keluaran sebelum writer snapshot
awal. Ini bukan klaim daemon INDEX, corpus nyata atau seluruh X01 selesai.

Tanggal: 2026-10-09 (Asia/Jakarta). Baseline Git `47b51b4`; perubahan dan SHA-256
file tercatat dalam `artifacts/verification/20261009-index-planning/results.json`.
Toolchain: Go 1.26.8 windows/amd64, PostgreSQL 16.8-alpine, Qdrant 1.18.0.
Backend disposable loopback tanpa volume corpus dipakai untuk integration test.

## Cakupan dan hasil

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Plan inventory | Setiap chunk tepat sekali; source/job duplikat, hash rusak, checkpoint/scope/snapshot salah, source partial, populasi salah ditolak | PASS |
| Determinisme/ownership | Retry input identik menghasilkan plan/ref identik; mutasi input/salinan tidak mengubah plan privat | PASS |
| Persistence | Real FileStore; kegagalan registrasi dependency tidak dianggap selesai; replay menghasilkan byte/ref/dependency identik | PASS |
| Output set | Banyak batch dan urutan delivery berbeda diterima; omission, duplicate plan dan substitusi plan ditolak | PASS |
| Worker boundary | Claim/request/context/lease, registered plan, source checkpoint, model producer, response fence, hash, checksum dan dictionary term membership diverifikasi; cancellation ditolak tanpa sukses palsu | PASS |
| Backend integration | Planner dan plan persistence sampai PostgreSQL/Qdrant publication, hidrasi dan draft fixture; lost write reply diretry; graph receipt yang diwajibkan tetapi hilang tetap memblokir publication | PASS |
| Full Go suite | `go test ./src/server/... -count=1`, backend nyata tersedia, exit 0 | PASS |
| Impacted suite setelah review | `go test ./src/server/internal/indexing -count=1`, PostgreSQL/Qdrant, exit 0 | PASS |
| Static checks | `go vet ./src/server/internal/indexing`, exit 0 | PASS |
| Corpus/model/gold/workload nyata | Belum menjalankan pipeline dari PDF corpus pengguna melalui native worker/model dan workload required pada perubahan ini | NOT_MEASURED |

Raw logs berada di folder run di atas: `integration.log`, `go-all-retry.log`,
`final-after-review.log`, dan `vet.log`. Run `go-all.log` sebelumnya gagal karena
Docker Desktop tidak berjalan dan koneksi kedua backend ditolak. Hasil gagal tetap
disimpan; setelah Docker dipulihkan suite diulang dan lulus. Kegagalan lingkungan
tersebut tidak diubah menjadi hasil PASS.

## Review independen

Agent `/root/verify_initial_index_planning` membaca diff dan menjalankan ulang tes
planning, persistence, dispatch dan artifact admission. Ia menemukan output sparse
dengan ID term asing dapat lolos boundary worker sebelum ditolak writer. Perbaikan
memakai dictionary terverifikasi di boundary worker dan writer; regression mengubah
term ID sekaligus menghitung ulang checksum/blob/checkpoint hash agar rejection
membuktikan membership gate. Review ulang menyatakan tidak ada temuan blocking
tersisa dalam cakupan library ini. Agent tidak menjalankan database integration
secara independen; integration log berasal dari implementer.

Cache byte output kini diteruskan ke admission writer, menghindari pembacaan ulang
setiap artefak embedding dan menjaga satu budget agregat 64 MiB serialized bytes.
Decoded protobuf dan salinan menambah memori; angka itu bukan batas RSS empiris.

## Prasyarat yang tetap terbuka

Caller tepercaya menentukan inventory, membekukan konfigurasi dan memilih seluruh
backend required. Library belum menyediakan persistent inventory, child-job scheduler
per plan, cancellation polling durable, recovery/checkpoint commit INDEX, atau wiring
daemon. `JobRecord` dari caller bukan mekanisme autentikasi endpoint publik.

Statistik diperiksa terhadap snapshot, policy, dictionary dan jumlah chunk; producer
statistik masih harus membuktikan population fingerprint/DF dari rendered input yang
tepat. Tidak ada pengurangan target, workload atau denominator. Angka required tetap
di [benchmark-targets.yaml](../configs/benchmark-targets.yaml), REQUIRED_UNMEASURED.
