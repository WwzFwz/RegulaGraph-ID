# Verifikasi batas receipt RESOLVE K01

Dokumen ini mencatat pemeriksaan validator request/response Registry.ResolveBatch pada 2026-09-24.
Perannya membedakan kelulusan boundary struktural dari keputusan registry PostgreSQL dan akurasi
identitas hukum yang belum tersedia. Input adalah artefak EXTRACT, kandidat registry terpin,
request LINK/DEFER, dan response keputusan; output validator hanya menerima receipt lengkap
yang terikat ke proposal, konteks, revision, canonical ID, dan correlation ID yang sama.

## Bukti dan hasil

Base revision: `1a6630a`. Fingerprint implementasi `resolution_receipts.go`:
`7fdd301bfda0b569448fa4d29f95811b6818bb76`; fingerprint tes:
`255b4df34e29ecc8f525539175dd79a1553c405e` (`git hash-object`).
Toolchain dicatat di `artifacts/verification/k01-registry-receipts-20260924/go-version.txt`.
Raw hasil akhir ada pada `go-test.log` dan `go-vet.log` di folder yang sama.

| Pemeriksaan | Hasil | Bukti dan batas |
| --- | --- | --- |
| Receipt LINK dan DEFER terikat proposal/kandidat | PASS | Tes terarah menerima keputusan valid dan menolak target, revisi, corpus, konteks, request, correlation, dan action yang bergeser. |
| Kelengkapan dan ID unik | PASS | Respons kosong/item error dan collision ID decision dengan proposal, mention, atau kandidat ditolak. |
| `go test ./...` pada `src/server` | PASS | Exit 0, raw `go-test.log`; seluruh paket Go yang tersedia diuji. |
| `go vet ./...` pada `src/server` | PASS | Exit 0, raw `go-vet.log`. |
| Review agent independen | PASS untuk scope validator | `verify_k01_receipts` menemukan collision ID, diperbaiki, lalu memeriksa ulang diff dan menjalankan tes terarah. |
| Autentikasi receipt PostgreSQL dan CAS revision | NOT_MEASURED | Adapter/writer semantic registry dan workflow durable RESOLVE belum tersambung. |
| Kualitas resolusi hukum dan benchmark required | NOT_MEASURED | Belum ada gold reviewed, model produksi, atau acceptance run. |

Perintah akhir memakai `GOCACHE` di bawah folder raw log karena cache default Windows tidak
dapat dibaca oleh sandbox. Kegagalan awal karena akses cache tercatat sebagai hambatan lingkungan,
bukan kegagalan assertion. Perintah yang selesai dengan exit 0 adalah:

```powershell
$env:GOCACHE = (Join-Path (Get-Location) 'artifacts/verification/k01-registry-receipts-20260924/go-cache')
go -C src/server test ./...
go -C src/server vet ./...
```

Validator bersifat murni dan mengasumsikan response datang dari adapter registry tepercaya.
Ia tidak membuktikan bahwa baris keputusan telah di-commit, revision belum berubah saat
publication, ataupun LINK benar secara hukum. Stage RESOLVE tetap belum aktif. Angka pada
`configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED** dan tidak diubah.
