# Verifikasi pemetaan sitasi A01

Dokumen ini merekam review boundary dari klaim jawaban ke Evidence, versi sumber, locator, dan URL metadata tepercaya. Ia menilai provenance struktural saja; kesesuaian makna klaim dengan pasal masih memerlukan gold dan review manusia.

## Bukti yang dijalankan

- Tanggal 2026-09-24; base revision `4a1690170acfdc2e6e155be98a640cf2ab2db6a3`; toolchain `go1.26.8 windows/amd64`. File terdampak: `src/server/internal/answering/citations.go`, `citations_test.go`, `src/server/internal/domain/boundaries.go`, serta README kedua komponen.
- `go test ./... -count=1` dari `src/server`: exit 0; `go vet ./...`: exit 0. Raw output tersimpan di `artifacts/verification/a01-citation-mapping-20260924/go-test.txt` dan `go-vet.txt`. `git diff --check`: exit 0. Fixture lokal mencakup versi/snapshot, bukti yang tidak dirender, judul URL palsu, sumber ganda, locator per blob, URL ber-*userinfo*, batas sitasi, lookup cache, ID duplikat, dan sitasi yang hilang.
- Agent verifikasi read-only menemukan bahwa klaim dengan dua evidence dapat lolos hanya dengan satu citation dan Evidence multisumber dapat kehilangan satu sumber. Implementasi diperbaiki agar setiap pasangan `(claim, evidence, source blob, provision version)` pada klaim SUPPORTED memiliki sitasi terverifikasi. Pemeriksaan ulang menyatakan celah ini tertutup. Agent juga menemukan perbedaan aturan URL antara builder dan gate akhir; aturan gate disamakan dan regresi hand-built Answer lulus.

## Batas hasil

`BuildCitations` aktif sebagai helper deterministik, belum terhubung ke generator atau endpoint produksi. Evidence.SourceRefs diperlakukan sebagai sumber yang semuanya perlu disitasi; kontrak belum menyatakan mirror alternatif, sehingga sumber tanpa locator/URL membuat pemetaan gagal tertutup. Lookup URL masih memakai `SourceURLLookup` yang belum membawa `context.Context`/snapshot secara eksplisit; wiring produksi harus menyediakan metadata snapshot-pinned yang telah di-*prefetch* atau memiliki deadline. Tes ini tidak membuktikan entailment, ketepatan hukum, kualitas jawaban, throughput, p95/p99, ataupun target benchmark required: semuanya **NOT_MEASURED**. Tidak ada PostgreSQL/model nyata pada pemeriksaan ini.
