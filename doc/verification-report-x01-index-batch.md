# Verifikasi closure lokal IndexBatch X01

Dokumen ini mencatat pemeriksaan validator batch indeks sejak revision dasar
`f0d4b3a`. Raw log berada di `artifacts/verification/20260925-x01-index-batch/`
(diabaikan Git). Gate ini hanya memeriksa konsistensi lokal sebelum mutation;
ia bukan izin publication.

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| `go test -count=1 ./src/server/internal/domain -run TestValidateIndexBatchLocalClosure` | PASS, exit 0; kasus count, sumber legal, generation, identitas record, target sequence, bentuk vector/model, konflik closure, dan corpus. Log `go-focused.log`. |
| `go test -count=1 ./...` dari `src/server` | PASS, exit 0; seluruh suite Go. Log `go-all.log`. |
| Checksum operasi, prior state, source hash/membership, fence, Qdrant nyata, publication CAS | NOT_MEASURED / belum diintegrasikan. |

Review independen awal menemukan target sequence campur dan bentuk vector yang
tidak terikat generation; implementasi dan regresi diperbaiki sebelum commit.
Validator memeriksa setiap record terhadap `IndexSourceView` tervalidasi,
menolak operasi ganda dalam batch, mengikat satu target sequence, dan mensyaratkan seluruh item yang dihitung
sebagai expected diterima. `Counts` menghitung record; batch closure-only
memakai nol record dan boleh melewati gate lokal bila berisi operasi sah.
State lama yang ditutup harus diperiksa oleh writer
otoritatif. Target required di `configs/benchmark-targets.yaml` tetap
**REQUIRED_UNMEASURED**; fixture ini tidak mengukur kualitas atau latency.
