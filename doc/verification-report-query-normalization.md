# Verifikasi normalisasi query mekanis

Dokumen ini mencatat bukti subkomponen Q01 pada 2026-10-09, berbasis revision
`05eb5dc` dengan fingerprint file final dalam manifest run. Ini bukan acceptance
Q01 keseluruhan atau bukti peningkatan akurasi. Kontrak tersedia pada
[normalisasi query](query-normalization.md).

## Input, proses, output

Pertanyaan UTF-8 maksimal 64 KiB dan policy operator masuk normalizer Go.
Expected: original-v1 identik; mechanical-v1 hanya whitespace/NFC di luar
kutipan, trace merekonstruksi hasil, dan normalisasi berulang stabil. Dense/BM25
menerima search form; graph, reranker dan generation menerima original.
Policy terikat fingerprint; mode tidak dikenal ditolak. Callback hidrasi tidak
dapat mengubah report caller. Actual sesuai tes berikut setelah perbaikan.

| Pemeriksaan | Bukti dan hasil |
| --- | --- |
| Unit normalizer | PASS: nomor/negasi, Unicode NFC, kutipan/apostrof/escape, typo/code-switch tetap, idempotence, trace, invalid UTF-8/NUL/blank/batas input dan ekspansi output |
| Workflow retrieval-answer | PASS: tiga branch menerima bentuk yang tepat; payload generator tetap original; hydration clone tidak mengubah trace caller |
| CLI/API | PASS: mode default/eksplisit/invalid, konfigurasi dan JSON trace CLI; tes runtime terkait ikut suite |
| Fuzz 20 detik, dua worker | PASS sesudah perbaikan, 10.735 eksekusi pada log terminal; bukan pembuktian seluruh kemungkinan input |
| Review independen | PASS terbatas oleh agent verify_index_abort; temuan combining-mark ditutup dengan regresi dan tes ulang |
| Microbenchmark lokal | 63.241 ns/op, 45,54 MB/s, 73.123 B/op, 203 allocs/op untuk input fixture 64 pengulangan; bukan p95/p99 atau gate release |
| Recall/nDCG, gold, acceptance workload | NOT_MEASURED; default tidak berubah, tidak ada target diturunkan |

Go 1.26.8, Windows/amd64; microbenchmark melaporkan AMD Ryzen 9 6900HS dan
GOMAXPROCS default 16. Fixture benchmark memuat aksen terurai, whitespace, nomor,
negasi dan kutipan. Backend/model pada tes workflow adalah doubles; tidak ada
klaim query terhadap corpus nyata atau performa backend dari run ini.

## Temuan dan perbaikan

Build awal menemukan benturan nama variabel mode di CLI serta literal Unicode
yang rusak saat penulisan script. Keduanya diperbaiki sebelum suite final.
Reviewer dan fuzz menemukan batas apostrof setelah combining mark tidak stabil:
`e\u0301'  x  '` berubah berbeda pada normalisasi kedua. Letter/digit/mark kini
memakai klasifikasi batas kata bersama. Tiga regresi mencakup reproducer reviewer,
reproducer fuzz, dan apostrof dalam kutipan. Fuzz ulang serta suite final lulus.
Header diperjelas agar tidak mengklaim spelling correction.

Raw logs dan manifest berada di
`artifacts/verification/20261009-query-normalization/`: packages-initial.log,
packages.log, packages-final.log, fuzz.log, fuzz-final.log, benchmark.log,
vet.log, static-checks.json, manifest.json. Kegagalan awal dipertahankan sebagai
riwayat; hanya hasil final mendukung klaim PASS.

## Reproduksi dan batas

```powershell
go test ./src/server/internal/retrieval/... ./src/server/internal/workflows ./src/server/internal/api/... ./src/server/cmd/api ./src/server/cmd/cli
go test ./src/server/internal/retrieval/query -run '^$' -fuzz '^FuzzNormalizeQuestionTrace$' -fuzztime=20s -parallel=2
go test ./src/server/internal/retrieval/query -run '^$' -bench '^BenchmarkNormalizeQuestion$' -benchmem -count=1
go vet ./src/server/internal/retrieval/... ./src/server/internal/workflows ./src/server/internal/api/... ./src/server/cmd/api ./src/server/cmd/cli
```

Trace HTTP, classifier intent, semantic query expansion, temporal CURRENT/COMPARE
dan evaluasi gold belum ditutup. Ikuti [benchmark-policy](benchmark-policy.md)
dan [verification-quality](verification-quality.md); hasil microbenchmark laptop
tidak menggantikan profil referensi required pada configs/benchmark-targets.yaml.
