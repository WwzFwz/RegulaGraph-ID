# Verifikasi envelope graph dan membership sumber

Dokumen ini mencatat pemeriksaan dua prasyarat coordinator ASSEMBLE: transform sumber
tanpa mengubah hasil model, serta bukti bahwa CHUNK bound benar-benar merupakan sumber
inventory snapshot terpublikasi. Kontrak dan pekerjaan lanjutan berada di
[coordinator graph](graph-assembly-coordinator.md). Ini belum completion K01 atau release.

## Revisi dan lingkungan

Implementasi envelope: `3a9c205`; membership dan shared inventory reader: `6932f52`.
Run 2026-10-09 menggunakan Go 1.26.8 windows/amd64, PostgreSQL 16.8 dan Qdrant 1.18.0
lokal disposable. Raw log berada di `artifacts/verification/20261009-graph-source-envelope/`.
Fixture CHUNK/EXTRACT/RESOLVE dan vector sintetis menguji invariant, bukan interpretasi
hukum atau kualitas model. Native model/worker lintas proses tidak dijalankan pada paket ini.

| Perintah / pemeriksaan | Expected dan hasil aktual | Log, exit |
| --- | --- | --- |
| `go test ./src/server/internal/workflows -run '^TestGraphSourceEnvelopes' -count=1 -v` | Empty/nonempty, LINK assignment, determinism dan dependency preservation lulus; drift/unknown/collision ditolak | `envelopes-final.log`, 0 |
| Reviewer independen rerun envelope setelah perbaikan | Diagnostic root remap dan unknown-ref rejection terverifikasi; PASS | `independent-final.log`, 0 |
| `go test ./src/server/internal/indexing -run '^TestPublishedGraphSourceMembershipAgainstStores$' -count=1 -v` | Publication PG/Qdrant nyata; exact membership diterima, delapan forgery dan released pin ditolak | `membership.log`, 0 |
| Reviewer independen rerun membership setelah SQL bounds | Published inventory/source receipt/pin terverifikasi; PASS | `independent-membership.log`, 0 |
| Reviewer memeriksa ulang tes inventory setelah refactor loader | Scheduling/restoration tetap lulus | `independent-inventory.log`, 0 |
| `go test ./... -count=1` dari src/server dengan DSN PG dan Qdrant endpoint | Semua package Go lulus, backend integration aktif | `go-all.log`, 0 |
| `go vet ./...` | Tidak ada temuan | `go-vet.log`, 0 |

Tes envelope memulihkan hanya field envelope dan mapping diagnostic root yang
didokumentasikan, lalu membandingkan seluruh payload dengan aslinya. Assignment
canonical, source/version/span, proposal/decision, recorded registry revision dan
producer model tidak berubah. Lookup observations dan seluruh dependency asli
dipertahankan exact. Artefak input juga tidak dimutasi. Hash, media, role source,
foreign job, altered document, previous snapshot, partial result, unknown fields,
root collision dan reference budget yang tidak cukup ditolak.

Tes membership melewati initial source binding, plan/inventory, output admission,
writer dan publication PostgreSQL/Qdrant nyata sebelum membaca membership. Scope,
source job, publication, fence, original ref, bound ref, snapshot hash dan lease
owner palsu ditolak. Lease yang sudah dilepas juga ditolak. Shared inventory decoder
memakai transaksi reader yang sama, tanpa mengambil koneksi pool lagi.

## Temuan dan perbaikan

Reviewer `verify_index_jobs` menemukan diagnostic warning yang merujuk root batch
lama menjadi tidak valid sesudah rebinding. Hanya dua jenis root reference tersebut
diremap; evidence record lain tidak berubah. Root/child collision ditolak agar mapping
tidak ambigu. Reviewer juga menemukan unknown fields pada input ArtifactRef dapat
terbawa ke output lalu ditolak Rust ASSEMBLE. Semua ref dan nested hash kini fail-closed;
regression dan rerun independen lulus. Tidak ada blocker review tersisa pada dua fungsi ini.

Percobaan awal gagal karena key storage fixture mengandung colon serta field wajib
reason/actor pada decision belum lengkap. Perbaikan fixture tidak melonggarkan validator.
Satu percobaan compile fixture diagnostic memakai field yang tidak ada pada schema;
diganti dengan field schema yang benar. `envelopes.log` juga merekam akses compiler
cache ditolak sandbox; rerun dengan akses toolchain lulus. Run gagal tidak dihitung PASS.

SQL memotong payload inventory berlebih sebelum transfer (per-plan/aggregate/ref dan
snapshot caps). Batas ini telah direview, tetapi fault injection oversized SQL payload
belum diuji terpisah. Benchmark RSS/p95/p99, concurrency produksi dan acceptance kualitas
tetap **REQUIRED_UNMEASURED**, mengikuti [target wajib](../configs/benchmark-targets.yaml).

## Sisa integrasi

Transform envelope masih helper murni. Belum ada receipt durable original-to-bound
EXTRACT/RESOLVE atau workflow yang mempersist dan menggabungkan seluruh gate sebelum
inventory ASSEMBLE. Membership initial-index adalah fakta snapshot lama; tidak memberi
otoritas menulis publication baru dan tidak membuktikan registry freshness. Dependency
BIND/EXTRACT lintas revision dan replan/reaffirmation eksplisit tetap diperlukan sebelum
melonggarkan guard revision GraphDelta. Writer Neo4j dan graph end-to-end belum selesai.
