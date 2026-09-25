# Verifikasi readback exact-ID Qdrant X01

Dokumen ini mencatat pemeriksaan helper readback Qdrant sejak revision dasar
`7536d14`. Raw log berada di
`artifacts/verification/20260925-x01-qdrant-readback/` (diabaikan Git).
Helper ini memeriksa satu batch ID terpin; ia belum menjadi bukti publication
atau kesiapan seluruh snapshot.

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| `go test -count=1 ./src/server/internal/adapters/qdrant` | PASS, exit 0; payload dan dense/sparse dibandingkan pada ID yang diminta, termasuk kasus hilang/berubah. Log `go-focused.log`. |
| `go test -count=1 ./...` dari `src/server` | PASS, exit 0; seluruh suite Go. Log `go-all.log`. |
| Review independen | Menemukan dua celah parsing: koordinat dense `null` dan angka snapshot pecahan yang terbulatkan. Keduanya diperbaiki dengan parsing bertipe dan `UseNumber`; regresi lokal lulus. |
| Qdrant hidup, replica/routing, manifest penuh, fault recovery dan benchmark | NOT_MEASURED; fixture hanya server HTTP sintetis. |

Readback memakai [API exact-ID Qdrant v1.18](https://api.qdrant.tech/v-1-18-x/api-reference/points/get-points)
dengan `consistency=all`, `with_payload=true`, dan `with_vector=true`. Ia
menolak hasil kurang/duplikat/asing, payload yang berbeda, dense vector yang
tidak sesuai normalisasi Cosine, dan sparse BM25 yang berbeda. Payload dibandingkan
dengan angka JSON presisi, bukan float64 yang dapat menyamakan sequence besar.

Untuk publication, coordinator masih harus memaginasi seluruh expected ID,
memastikan hash/manifest serta membership sumber, menguji seluruh route/replica
yang boleh melayani traffic, dan mengaitkan hasil ini dengan operation ledger,
fence serta CAS snapshot. Readback per batch ini tidak membuktikan semua itu.
Required latency, throughput, Recall@k dan kualitas hukum tetap **NOT_MEASURED**.
