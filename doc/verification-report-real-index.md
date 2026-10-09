# Verifikasi pemulihan INDEX dan query PDF nyata

Dokumen ini mencatat paket operasional 2026-10-09: menutup inventory gagal melalui
CLI, mengindeks PDF PP 12/2006 dengan model nyata, menerbitkan snapshot, dan membaca
bukti melalui query produksi. Ini bukan acceptance seluruh GraphRAG atau benchmark.
Baseline `241ccfc`; fingerprint file implementasi dan runtime pada
`artifacts/verification/20261009-index-recovery/run-manifest.json`.

## Input, runtime dan jejak run

Input PDF SHA-256 `c2c761194c0a15d1858c38c7c45308e045539a80be5fd67b23cd4a050f7dbbfb`,
record acquisition dan CHUNK terverifikasi sesuai [handoff](operational-handoff.md).
Satu PDF menghasilkan 35 chunk, populasi lexical 1.789 token. Runtime memakai Go
1.26.8 Windows/amd64, Rust worker `worker-v3.exe` dari run real-PDF sebelumnya,
C++ ONNX Runtime 1.22.0 CUDA dengan BGE-M3 FP16, CLS/L2 dan dimensi 1024. Binary
lama dicatat hash-nya; tidak diklaim dibangun dari revision baru. Hash manifest
model/config dan binary berada pada manifest run; model besar tidak masuk Git.

DB schema operasional `regulagraph_ops_pp12_v1`; test PostgreSQL memakai schema
terpisah `regulagraph_verify_abort_20261009`. DSN tidak disimpan dalam laporan.
Qdrant lokal `http://127.0.0.1:56348`, native `127.0.0.1:55072`, worker
`127.0.0.1:55071`. Artifact root `artifacts/operational/pp12-v1/objects`.
EXTRACT/RESOLVE/ASSEMBLE tidak diaktifkan pada corpus ini.

## Kegagalan dan perbaikannya

Publication `publication:pp12-index-v1` gagal menjalankan INDEX karena Rust native
endpoint tidak memiliki prefix `http://`. Coordinator terlanjur hidup tanpa
worker sehingga seluruh delapan attempt habis. Tidak ada backend operation pada
ledger publication itu. Error/log dan job FAILED tetap disimpan.

CLI baru `abort-index` memeriksa corpus inventory, lalu memakai coordinator dan
transaksi abort yang sudah ada. Snapshot published atau ledger planned/applied
ditolak; job, sumber dan active snapshot tidak dihapus/reset. Replay pada ABORTED
tetap error, bukan idempotent success. Command tidak mengompensasi backend.
Publication v1 kini ABORTED. Replan melalui `prepare-snapshot`, vocabulary,
`prepare-dictionary`, freeze dan `prepare-index` membuat v2 dengan dependency
snapshot baru. Native/worker dipastikan menerima koneksi sebelum coordinator.

INDEX v2 berhasil pada attempt pertama dan child STAGED. `publish-index -profile
hybrid` berhasil melalui admission/write/readback/receipt dan activation produksi.
Snapshot aktif:

```text
publication:pp12-index-v2
generation:pp12-index-v2
initial-source-snapshot-v1:2201330fd365e1f7c6b966a30fcc49714dc5647c6077aedb5ba07ee3bac585e1
collection: regulagraph_pp12_operational_v2
```

Query awal kemudian gagal `hydrate candidate evidence: record not found`.
Hydrator mewajibkan normalized text mempunyai row registry terpisah, sedangkan
worker menyimpan descriptor-nya dalam DocumentBatch. Perbaikan mengikuti kontrak
pembaca EXTRACT/RESOLVE: root plan/DocumentBatch tetap memerlukan registry;
normalized bytes hanya dibaca melalui dokumen yang sudah admitted, dengan hash,
size, descriptor, byte budget dan UTF-8 span tetap diperiksa. Root read memeriksa
registry sebelum cache, termasuk bila nested bytes sudah berada di cache.
Tidak ada fallback umum untuk registry miss atau backfill data manual.

## Pemeriksaan dan hasil

| Pemeriksaan | Hasil dan batas bukti |
| --- | --- |
| `go test ./src/server/cmd/cli -count=1` | PASS, exit 0; wiring abort, argumen, redaction, deadline dan output |
| `go test ./src/server/internal/adapters/postgres -run '^TestStorageFoundationAgainstPostgres$' -count=1 -v` | PASS, exit 0 pada DB nyata terisolasi; mencakup planned/applied compensation guard dan preservation active snapshot |
| `go test ./src/server/internal/retrieval/... ./src/server/internal/workflows ./src/server/internal/indexing ./src/server/cmd/cli -count=1` | PASS, exit 0; integration opt-in tanpa environment tetap SKIP, bukan tambahan klaim DB/native |
| `go vet ./src/server/internal/retrieval/... ./src/server/cmd/cli` | PASS, exit 0 |
| `TestHydrationNestedTextAuthority` | PASS: nested tanpa registry, root missing/mismatch setelah cache, dokumen/teks asing, corrupt bytes, budget, descriptor drift |
| `TestOperationalPublishedQuery` dengan corpus/model nyata | FAIL sebelum perbaikan; PASS sesudah perbaikan dan setelah penghapusan diagnostic raw error dari test |
| CLI `publish-index` dan `query-evidence` | PASS, exit 0; evidence benar-benar dari published snapshot v2, bukan fixture/mock |
| State database akhir | v1 ABORTED, v2 PUBLISHED; child v1 tetap FAILED attempt 8, child v2 STAGED attempt 1; read leases nol |
| Gold, model quality, latency/throughput required | NOT_MEASURED; tidak ada target yang diubah |

Pertanyaan umum “Apa yang diatur dalam peraturan pemerintah ini?” menghasilkan
29 item, `COMPLETION_STATUS_SUCCEEDED` dan `COMPLETENESS_PARTIAL` dengan 38 missing
dependencies. Item memiliki source/version refs dan page locators; teks yang
terambil memuat PP 12/2006 tentang perubahan pengenaan pajak penjualan barang
mewah. Pertanyaan tanggal berlaku juga berhasil dieksekusi. Ini bukti pembacaan
dan provenance, bukan skor relevansi atau pengesahan interpretasi hukum. Query
belum menjalankan generation jawaban.

Review independen `/root/verify_index_abort`: PASS scoped untuk CLI abort dan
perbaikan hidrasi, tanpa temuan blocking. Reviewer menjalankan sembilan subkasus
abort serta delapan kasus nested-artifact dan enam belas kasus temporal. Bukti
query nyata diperiksa dari log/JSON, tidak dijalankan ulang oleh reviewer. Minor
temuan usage/README abort diperbaiki; final component tests/vet lulus sesudahnya.

## Artefak dan kelanjutan

Raw tests dan manifest: `artifacts/verification/20261009-index-recovery/`.
Log run nyata: `artifacts/operational/pp12-v1/logs/`, terutama
`abort-index.json`, `prepare-index-v2.json`, `coordinator-index-v2.stdout.log`,
`publish-index-v2.json`, `query-hybrid-v2.json`, `query-effective-v2.json`.
Redirect PowerShell menghasilkan UTF-16; generated wire `.pb` tetap biner C01.
Gagal sandbox/cache awal tidak dihitung sebagai test PASS; rerun yang tercatat
berjalan dengan akses toolchain yang sesuai.

Coordinator dan worker selesai dan dihentikan secara terarah. Native masih
disediakan untuk query; periksa PID/path aktual sebelum stop atau reuse.
[Handoff](operational-handoff.md) memuat cara resume. Berikutnya adalah generator
lokal pada corpus ini dan cabang EXTRACT/RESOLVE/graph nyata. Missing dependencies,
canonical lifecycle, OCR dan acceptance lainnya tetap pekerjaan proyek penuh.
