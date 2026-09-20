# Verifikasi incremental change planner I01

Dokumen ini mencatat bukti verifikasi planner perubahan incremental I01 pada 2026-09-20. Perannya
mengikat invalidasi berbasis content, producer, dependency, dan lookup revision ke implementasi serta
counterexample yang dapat ditinjau. PASS hanya berlaku untuk scope library planner; equivalence terhadap
full rebuild dan target benchmark produksi tetap **REQUIRED_UNMEASURED**.

## Identitas revision

Implementasi yang diverifikasi tersimpan pada commit
`6f500419ca35daa0eea809b686c2d83de72d4353`, dengan preflight resource cap final pada
`ecfa0bea02e6ad26dbbb4f800102c9d9327ba400`. Target, workload, denominator, serta asumsi hardware dalam
`configs/benchmark-targets.yaml` tidak diubah.

Fingerprint SHA-256 `src/ingestion/src/document/change_detection.rs` yang diaudit adalah
`1fd4c3d1ab92ae4aff2253adc3bf45ec7c25ce6f77f48dc04a6634b2a950cb8e`.

## Invariant yang dibuktikan

| Boundary | Hasil | Bukti utama |
| --- | --- | --- |
| Deteksi perubahan | PASS | Content, producer, dependency fingerprint, dan lookup revision memicu recompute |
| Reuse | PASS | Item identik masuk `reuse_set`; urutan input tidak mengubah output |
| Dependency closure | PASS | Reverse closure menjangkau consumer transitif, multiple roots, dan cycle tanpa loop |
| Removal | PASS | Item hilang masuk review, consumer aktif direcompute, dan bukti independen tetap reusable |
| Fingerprint integrity | PASS | Konflik fingerprint global dan hash dependency lokal yang tidak cocok target ditolak |
| Lookup integrity | PASS | Konflik revision untuk scope yang sama ditolak dan negative lookup dipertahankan |
| Resource limit | PASS | Item/edge cap, overflow, duplicate ID, hash, serta identitas diperiksa sebelum plan diterima |
| Wire output | PASS | `UpdatePlan` tervalidasi C01, terpartisi, dan diurutkan secara deterministik |

Traversal closure memakai hash index dan queue dengan kompleksitas linear terhadap item dan edge.
Sorting hanya dilakukan pada hasil wire agar output stabil. Planner menghitung gabungan edge kedua
snapshot sebelum membangun indeks, sehingga input yang melampaui cap ditolak sebelum alokasi indeks;
peak RSS resmi tetap belum diukur.

## Pemeriksaan implementer

Perintah berikut lulus pada implementasi final:

```powershell
cargo test --workspace --all-targets
cargo clippy --workspace --all-targets -- -D warnings
cargo test --manifest-path .cache/wire-review/Cargo.toml --locked --offline --lib incremental -- --test-threads=1 --nocapture
git diff --check
```

Suite workspace menghasilkan 81 unit PASS, satu fixture interop ignored yang dijalankan melalui harness
kontrak, dan satu native PDFium integration PASS. Clippy lulus dengan warning sebagai error. Harness
independen menghasilkan 9/9 PASS, termasuk edge-cap precedence dan determinisme pada 20 permutasi.

## Review independen

Agent `verify_c01` mengaudit implementasi secara read-only. Tujuh counterexample awal dijalankan oleh
reviewer dan seluruhnya PASS. Dua counterexample tambahan disiapkan reviewer lalu dijalankan implementer
karena eksekusi `rustc` reviewer terhalang sandbox; keduanya PASS. Review statis tidak menemukan bug
material tersisa pada scope planner dan menetapkan verdict **PASS**.

## Batas pembuktian

Audit ini belum membuktikan incremental/full-rebuild equivalence pada corpus nyata, integration worker,
checkpoint dan crash recovery, publication atomik end-to-end, freshness, throughput, latency p95/p99,
atau peak RSS pada workload referensi. Seluruh target numerik terkait tetap **REQUIRED_UNMEASURED** sampai
acceptance run resmi tersedia.
