# Verifikasi penundaan claim EXTRACT pada coordinator

Dokumen ini mencatat opsi penjadwalan yang memungkinkan persiapan dokumen/indeks
berjalan tanpa memanggil EXTRACT sebelum profil model siap. Opsi tidak mengubah
hasil transformasi, durable job, requirement graph atau workload benchmark.

`REGULAGRAPH_EXTRACT_ENABLED` kosong/`true` mempertahankan default lama. `false`
mengisi `ParseExecutorConfig.DisableExtraction`; nilai lain ditolak saat startup.
Executor menghilangkan hanya `ClaimExtractJob` dari rotasi dan tetap mencoba
PARSE/STRUCTURE/CHUNK. BIND serta kelompok INDEX/RESOLVE/ASSEMBLE tidak berubah.
Restart dengan `true` memungkinkan pending job diklaim dan diverifikasi normal.
Tidak ada migrasi/schema baru dan tidak ada mutasi status untuk menunda pekerjaan.

Flag hanya memengaruhi proses coordinator tersebut setelah restart. Coordinator
lain yang enabled tetap dapat claim, dan perubahan environment tidak membatalkan
RPC yang sudah berjalan. Penyelesaian EXTRACT, publication, dan graph tidak boleh
disimpulkan dari opsi ini. Semua target numerik required tetap berlaku.

## Bukti

Base revision `97f6da3fab90ff4ac72974bec6f549f796333cb5` plus perubahan yang
fingerprint-nya tercatat pada `independent-results.json`. Toolchain Go 1.26.8
windows/amd64. Raw evidence:
`artifacts/verification/20261009-extraction-suspension/`.

| Pemeriksaan | Hasil | Bukti |
| --- | --- | --- |
| `go test ./src/server/internal/workflows ./src/server/cmd/ingestion-worker -count=1` | PASS, exit 0 | `tests.log` |
| `go vet` pada kedua package tersebut | PASS, exit 0 | `vet.log` |
| Review independen dan tes kedua package dengan `-count=1 -v` | PASS_SCOPED, exit 0 | `independent.log`, `independent-results.json` |
| DB nyata/native worker dengan suspension; indeks corpus aktual | NOT_MEASURED pada paket ini | Pekerjaan integrasi berikutnya |
| Kualitas model dan acceptance benchmark | NOT_MEASURED | Tidak diklaim oleh flag scheduling |

Regression cases memastikan flag default tetap enabled, typo ditolak, claim
EXTRACT tidak dipanggil selama enam iterasi, worker/state/checkpoint/catalog tidak
disentuh, tiga tahap lain berotasi, error storage tidak disembunyikan, serta
executor baru enabled memproses job fixture yang sama melalui gate lama.
Fixture store/worker membuktikan perilaku scheduler, bukan database recovery.

Cara memakai dan batas operasional ada di
[README coordinator](../src/server/cmd/ingestion-worker/README.md) dan
[handoff operasional](operational-handoff.md). Berikutnya jalankan source nyata
sampai CHUNK dengan scope/identitas persisten, siapkan vocabulary/statistics dan
INDEX, lalu publication dense/BM25. EXTRACT/RESOLVE tetap pekerjaan tertunda yang
diperlukan sebelum graph dapat diterbitkan.
