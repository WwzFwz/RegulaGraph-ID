# Verifikasi timeline ketentuan I01

Dokumen ini mencatat bukti verifikasi validator dan selector timeline ketentuan I01 pada 2026-09-20.
Perannya mengikat interval berlaku, status review, change-event, provenance, dan unresolved policy ke revision
yang dapat ditinjau. PASS hanya berlaku untuk scope library versioning; rekonstruksi hukum dari corpus dan
target benchmark produksi tetap **REQUIRED_UNMEASURED**.

## Identitas revision

Implementasi yang diverifikasi tersimpan pada commit
`84745df80ab0a73942e29376fb4f694155988bb8`. SHA-256
`src/ingestion/src/document/versioning/provisions.rs` adalah
`84f3c7e1612dd3ec09a33029b6dee27e55818255c2333d560f00668f610edd6d`. Target, workload,
denominator, dan asumsi hardware dalam `configs/benchmark-targets.yaml` tidak diubah.

## Invariant yang dibuktikan

| Boundary | Hasil | Bukti utama |
| --- | --- | --- |
| Identitas | PASS | Provision logis terpisah dari version; duplicate, self-lineage, wrong provision, dan cross-corpus ditolak |
| Interval | PASS | Seleksi memakai interval half-open `[start,end)` dan mendukung batas unbounded |
| Ketidakpastian | PASS | UNKNOWN/CONFLICT date tidak ditebak; EXCLUDE/REPORT/REQUIRE_REVIEW menghasilkan keputusan eksplisit |
| Status | PASS | Rejected/quarantined dikeluarkan; unreviewed/unknown legal status memerlukan review; declared conflict tidak disamarkan |
| Overlap | PASS | Overlap approved bertanggal pasti harus dideklarasikan sebagai conflict, termasuk interval unbounded |
| Change-event | PASS | AMEND/INSERT/REPEAL/RENUMBER terikat ke boundary versi yang sesuai dan effective date unbounded ditolak |
| Provenance | PASS | Event tanpa source/locator/span nonkosong serta replacement span kosong ditolak |
| Resource | PASS | Batas version/event/support diperiksa sebelum validasi dan alokasi indeks besar |
| Determinisme | PASS | Timeline dan event diurutkan canonical; urutan input tidak memengaruhi keputusan |

Lookup event dan affected-provision memakai hash index. Build timeline berjalan linear terhadap record/edge
ditambah sorting, sedangkan selection saat ini memindai versi dalam satu provision. Workload corpus dan peak
RSS belum diukur sehingga tidak ada klaim target performa.

## Pemeriksaan implementer

Perintah berikut lulus pada implementasi final:

```powershell
cargo test --workspace --all-targets
cargo clippy --workspace --all-targets -- -D warnings
cargo test --manifest-path .cache/wire-review/Cargo.toml --locked --offline --lib versioning -- --test-threads=1 --nocapture
git diff --check
```

Suite workspace menghasilkan 90 unit PASS, satu fixture interop ignored yang dijalankan melalui harness
kontrak, dan satu native PDFium integration PASS. Clippy lulus dengan warning sebagai error. Harness
independen menghasilkan 10/10 PASS.

## Review independen

Agent `verify_c01` mengaudit source secara read-only. Review awal menemukan policy invalid yang dapat lolos,
tanggal event yang tidak konsisten dengan interval, lookup relasi yang belum terindeks, serta replacement dan
provenance span kosong yang masih dianggap bukti. Implementasi diperbaiki dan regression test ditambahkan.
Audit akhir memberi verdict **PASS untuk scope library versioning** tanpa temuan material terbuka.

## Batas pembuktian

Audit ini belum membuktikan bahwa change-event hasil extraction benar terhadap PDF/HTML resmi, ketepatan
canonical provision registry, gold temporal accuracy, incremental/full-rebuild equivalence, worker dan
publication end-to-end, latency p95/p99, throughput, atau peak RSS pada workload referensi. Seluruh target
numerik terkait tetap **REQUIRED_UNMEASURED** sampai acceptance run resmi tersedia.
