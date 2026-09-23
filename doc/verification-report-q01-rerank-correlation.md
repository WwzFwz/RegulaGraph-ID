# Verifikasi Q01: korelasi batch reranker

Dokumen ini mencatat uji pemetaan hasil reranker ke kandidat fusion tanpa membuang bukti. Basis sebelum perubahan `2153ddc0a524d3f062fceac00c4e1b0ccf8e160f`; SHA-256 `reranking.go` `CF575BCAB8C9788FFA09EAD40097D86D52272DB428D16FA01B85227E8B2D4EEE` dan `reranking_test.go` `309937562E5EA372D629671072CC74DBF6A668AE11A8C1843795E69FDAEB022A`. Raw log: `artifacts/verification/q01-rerank-20260924/` (diabaikan Git). Lingkungan Go 1.26.8 windows/amd64, fixture skor sintetis tanpa model.

| Pemeriksaan | Status | Expected dan aktual |
| --- | --- | --- |
| `go test ./... -count=1` dan `go vet ./...` | PASS, exit 0 | Seluruh paket Go dengan tes lulus, vet tanpa temuan; log `go-test.log`, `go-vet.log`. |
| Korelasi batch reranker | PASS pada fixture | Response terbalik tetap melekat ke pair/evidence benar; hasil hilang/duplikat/gagal, salah request/model, skor nonfinite, token di luar batas, dan truncation inkonsisten ditolak. Skor seri mempertahankan urutan fusion. |
| Review independen | PASS dalam cakupan korelasi | Reviewer menemukan alias provenance input-output; nested provenance kini disalin dan regression mutasi input lulus. Tidak ada blocker tersisa di fungsi ini. |
| Model native, binding pair–teks–snapshot, nDCG/coverage, p95/p99 | NOT_MEASURED | Service model, indeks, gold, serta workload referensi belum terhubung. |

Fungsi ini menerima daftar pair yang disiapkan caller, sehingga caller wajib memastikan teks query/dokumen dan snapshot sesuai kandidat sebelum mengirim batch. `InputTokens` dan `RetainedTokens` dibatasi oleh `ModelManifest.max_tokens`, tetapi kesetaraan kedua angka menunggu definisi tokenizer khusus model. Ambang benchmark required tidak diubah dan tetap **REQUIRED_UNMEASURED**.
