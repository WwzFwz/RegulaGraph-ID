# Verifikasi handoff EXTRACT ke RESOLVE K01

Dokumen ini mencatat bukti submilestone handoff RESOLVE pada 2026-09-24: job hanya
diklaim dari checkpoint EXTRACT sukses, kedua artefak dibaca melalui `ReadVerified`,
dan writer registry memeriksa ulang lease, fence, checkpoint, serta hash sumber di
dalam transaksi CAS. Cakupan ini belum mencakup producer kandidat/review produksi,
output dan recovery RESOLVE, kualitas resolusi hukum, atau acceptance release.

Basis revisi adalah `24f8f3c` pada branch `main`; perubahan berada pada commit
`6f3faec` (fixture storage) dan `ebc5296` (handoff). Fingerprint working tree
`git hash-object` berikut sebelum commit: `jobs.go`
`b3264981c31a093d0575a8c0dee184bdb3c649e2`, `registry_semantic.go`
`ea3f69c509fe6e8a804f15a6308f5e34447cb3a0`, `registry_semantic_fence.go`
`20daeae06678aceff399577e1d1d220afa30ea82`, `semantic_registry.go`
`a5337be516b1f75df044555ea921591574d78fef`, dan
`semantic_resolution.go` `f4b32a94f06cf32273489bf4e7c1059cabbc62c8`.
Log mentah berada di `artifacts/verification/k01-resolve-handoff-20260924/`.
Lingkungan: Go 1.26.8 windows/amd64, PostgreSQL 18.0 disposable pada
`127.0.0.1:55439/regulagraph_k01h`; suite integrasi menerapkan migration pada
database fixture. Data uji adalah EXTRACT/kandidat sintetis berhash, checkpoint,
review LINK/DEFER, dan `FileStore` lokal; tidak ada gold corpus hukum.

| Pemeriksaan | Status | Expected dan hasil aktual |
| --- | --- | --- |
| Klaim job | PASS | `ClaimResolveJob` menolak checkpoint EXTRACT tanpa terminal sukses; klaim dengan checkpoint sukses memberi lease RESOLVE. Claim generik tidak mengambil RESOLVE. |
| Input dan fencing | PASS | Workflow memeriksa corpus, stage, fence, hash/ref, metadata kandidat, dan batas byte sebelum commit. Writer PostgreSQL menolak stale fence, checkpoint/hash/manifest rusak, cancellation atau expiry sesudah pembacaan, dan owner lama yang mencoba replay. |
| Expiry selama transaksi | PASS | Tes memblokir writer pada review row sampai waktu lease database lewat. Pemeriksaan ulang sebelum commit menolak LINK dan jumlah operation row tetap nol. |
| Replay valid | PASS | Handoff `FileStore` ke writer menyimpan LINK/DEFER; replay dengan proof hidup mempertahankan revision dan decision ID. |
| Pengujian Go | PASS | `go test ./internal/adapters/postgres -run 'TestSemanticRegistryAgainstPostgres|TestStorageFoundationAgainstPostgres' -count=3`, exit 0, `go-test-repeat.log`; `go test ./... -count=1`, exit 0, `go-test-final.log`. Satu run awal sebelum perbaikan fixture lease 200 ms gagal pada assertion concurrent claim yang sensitif terhadap scheduling; fixture kini memakai lease satu menit dan expiry eksplisit, lalu kedua run final lulus. |
| Pemeriksaan statis | PASS | `go vet ./...`, exit 0, `go-vet-final.log`; `git diff --check` dan `gofmt` tanpa temuan. |
| Review independen | PASS untuk scope handoff | Agent `verify_k01_fenced_handoff` membaca diff/desain dan regresi expiry, lalu tidak menemukan blocker integritas lokal. Reviewer tidak menjalankan PostgreSQL sendiri. |
| Kualitas dan performa release | NOT_MEASURED | Tidak ada gold false merge/split atau workload referensi untuk p50/p95/p99, throughput, lock wait, dan peak memory. Target `configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**. |

Risiko performa yang perlu diprofilkan: verifikasi artefak menghitung hash saat membuka
dan membaca, lalu decoder writer menghitung hash lagi; `FOR SHARE` pada job dapat
menahan `RenewLease` selama transaksi. Lease harus diperpanjang sebelum batch panjang,
dan waktu lock serta konflik perlu diukur. Tahap lanjutan memerlukan producer
kandidat/proposal produksi, review terautentikasi, output/checkpoint/recovery RESOLVE,
graph assembly, serta evaluasi kualitas dan performa. Handoff ini tidak boleh
dianggap sebagai stage RESOLVE lengkap.
