# Verifikasi registrasi alias bersumber

Laporan ini mencatat bukti boundary `review-alias`: mention EXTRACT yang sukses
dihubungkan melalui review operator ke identity BIND existing, lalu menjadi
kandidat RESOLVE yang sumbernya dapat dihidrasi. Cakupan ini tidak membuktikan
CREATE semantik, kualitas model, legal validity atau graph corpus nyata selesai.

Baseline: `43786d7`; implementasi library/transaksi pada `9d0f907`, CLI pada
`46d58c7`. Go
`go1.26.8 windows/amd64`, PostgreSQL lokal pada port 55448. Tes database membuat
schema `review_<timestamp>` sendiri dan menghapus schema test itu melalui cleanup;
tidak memigrasikan atau mengubah schema corpus operasional. Daftar hash file dan
perintah/exit code berada pada
`artifacts/verification/20261009-sourced-alias/manifest.json` (diabaikan Git).

## Hasil pemeriksaan

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Exact source/target | BIND materializer menghasilkan identity; quote/normalized alias tetap persis, konteks dan target berbeda terlihat; foreign corpus/scope, wrong type, missing target/mention, stale revision dan hash/text mismatch ditolak | PASS |
| PostgreSQL commit dan konsumsi | Profil/alias/review menjadi satu revision; candidate builder membaca alias baru; hydrator RESOLVE aktual menemukan mention/konteks dari FileStore | PASS |
| Recovery/replay | Inspeksi baru pada revision lama mengembalikan hash sama setelah profil pertama dibuat; operation sama mereplay, actor berubah dan review ledger hilang ditolak | PASS |
| CAS/rollback | Stale write, competing writers, source catalog hilang, policy request salah dan target key berubah ditolak; fault di final review insert tidak meninggalkan profile/alias/operation/review atau revision parsial | PASS |
| Profil immutable | Alias identik tidak menaikkan revision; tambahan identity key palsu pada historical profile ditolak | PASS |
| CLI | Inspect tidak menerima approval flags; accept memerlukan hash/revision/operation/reason; deadline dan redaksi error serta stdout failure diperiksa | PASS |
| Regression empat paket | Domain, postgres, workflows dan CLI lulus; tes DB opt-in tidak dihitung dari run tanpa DSN ini | PASS |
| Static/schema | `go vet` empat paket lulus; `check_contracts.py`: 173 message, 32 enum, 4 service, baseline tidak ditulis ulang | PASS |
| Independensi | Reviewer `/root/verify_index_abort` memeriksa diff/log/tes serta menjalankan unit/CLI terfokus; DB pada sesi reviewer SKIP karena DSN tidak tersedia | PASS terbatas review kode dan bukti implementer |
| Corpus nyata, kualitas, benchmark | Fixture EXTRACT sintetis; tidak menjalankan model, acceptance gold/performa atau publication graph | NOT_MEASURED |

Raw log: `exact-gates.log` (delapan skenario PostgreSQL dan unit source/target),
`go-packages.log`, `vet.log`. `integration.log`/`integration-final.log` menyimpan
run sukses sebelum assertion diperketat. Fixture request awal belum memuat
`portal_id` dan satu test mempertahankan error dari pemeriksaan negatif; keduanya
diperbaiki sebelum run final. PASS final bukan diambil dari penolakan fixture
invalid: valid commit/hydration harus sukses, dan assertion negatif terakhir
memeriksa gate source/CAS/final rollback yang dimaksud.

Reviewer menemukan dua masalah audit: bukti target belum terlihat jelas dan
ledger belum menyimpan target/policy secara eksplisit. Preview sekarang memuat
DocumentBatch target beserta artifact ref; label operator ditandai terpisah.
Migration/receipt menyimpan target artifact dan policy fingerprint. Reviewer
menyatakan kedua temuan tertutup dan tidak menemukan blocking issue tersisa pada
runtime. Pengetatan assertion tes sesudah review tidak mengubah runtime.

## Reproduksi

```powershell
go test ./src/server/internal/domain ./src/server/internal/adapters/postgres ./src/server/internal/workflows ./src/server/cmd/cli
# Isi DSN database TEST, bukan schema operasional; helper membuat schema terisolasi.
$env:REGULAGRAPH_TEST_POSTGRES_DSN = '<DSN PostgreSQL test>'
go test ./src/server/internal/workflows -run '^TestSourcedAlias' -count=1 -v
go vet ./src/server/internal/domain ./src/server/internal/adapters/postgres ./src/server/internal/workflows ./src/server/cmd/cli
python scripts/check_contracts.py
```

Jangan mengasumsikan run paket PostgreSQL lain aman diarahkan ke schema produksi:
sebagian tes lama memakai TRUNCATE. Run DB milestone ini memakai helper schema
terisolasi di workflows. [Panduan operator](sourced-alias-review.md) menjelaskan
penggunaan nyata dan kebutuhan EXTRACT sukses, yang belum tercapai pada corpus
contoh lokal. Entitas baru di luar identity BIND, replan otomatis dan reuse alias
lintas snapshot tetap pekerjaan lanjutan; model lebih baik tidak menghapusnya.
