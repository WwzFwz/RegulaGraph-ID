# Verifikasi API evidence

Dokumen ini mencatat boundary HTTP dan runtime query reusable pada 2026-10-09
di atas revision `faa73af`. Logs/fingerprint berada di
`artifacts/verification/20261009-evidence-api/`. Cakupan adalah evidence vector/hybrid,
bukan generator jawaban, graph, deployment atau kelulusan benchmark.

| Pemeriksaan | Bukti |
| --- | --- |
| Authorization dan request | PASS: token hilang/salah/duplikat, Origin, corpus asing, unknown/duplicate/trailing JSON, ukuran, profile/mode ditolak sebelum dispatch |
| Error dan admission | PASS: overload 429, deadline diteruskan, slot dilepas, output nil/asing ditolak dan error backend disamarkan |
| HTTP lifecycle | PASS: loopback wajib, liveness terpisah readiness, Shutdown dengan listener nyata menunggu request aktif |
| Native integration | PASS: Rust population dan INDEX, C++ BGE-M3, PG/Qdrant, dua cold query HTTP concurrent, readiness dan dua warm query; tidak ada read lease tersisa |
| Independent review | PASS: `/root/verify_index_jobs`, `independent-final-unit.log` dan `independent-final-native.log` |
| Race detector | BLOCKED: Cygwin GCC gagal signal pipe dalam sandbox, lalu compiler cgo type/format mismatch pada run elevated; tidak ada hasil race PASS |
| Gold, benchmark required, generator/graph quality | NOT_MEASURED |

Run concurrent pertama menemukan bundle ID `evidence:hydrated` yang konstan.
Perbaikan mengikat ID ke trusted read lease, corpus/snapshot dan deterministic
QuestionRequest. Item bukti tidak diganti ID-nya. Tes ulang membuktikan ID bundle
berbeda serta tabel lease kosong. Request ID HTTP dibuat server dan diteruskan
lewat context privat, tidak menerima nilai header caller. Finding collision CLOSED.

Perintah utama: `go test ./src/server/internal/api/... ./src/server/cmd/api -count=1`
dan native opt-in `go test ./src/server/internal/indexing -run '^TestNativeIndexPipeline$'
-count=1 -v` dengan environment pada [laporan native](verification-report-native-index.md).
`lifecycle.log`, `identity.log`, `native-concurrent.log` (kegagalan), dan
`native-concurrent-fixed.log` menyimpan urutan hasil tanpa menghapus run gagal.
Go1.26.8 Windows amd64, PostgreSQL16.8, Qdrant1.18.0, Rust dev worker dan ONNX
Runtime1.22.0/BGE-M3 FP16 lokal. Source fixture terdiri atas enam chunk, bukan
human-reviewed gold; lama fixture test tidak dipakai sebagai latency benchmark.

Cache generation dan fresh lease diperiksa melalui proses backend nyata. Rollover
lintas generation masih menggunakan guard library yang ada; native HTTP run ini
tidak mengganti model/snapshot di tengah request. Tidak ada klaim stress/load
atau semua bentuk slow-client failure sudah diuji. Cara penggunaan ada pada
[panduan operator](evidence-api.md).
