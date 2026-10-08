# Verifikasi worker ASSEMBLE

Laporan ini mencatat boundary worker Rust pada 2026-10-09, kode handler/test
`85e5d3e` dan bootstrap `ddd3364`. Raw logs berada di
`artifacts/verification/20261009-assembly-worker`. Cakupan: plan terpin, pembacaan
artefak nyata, GraphDelta/checkpoint, cancellation dan service in-process; bukan
publication graph, correctness hukum atau benchmark release.

## Lingkungan dan perintah

Windows amd64, Rust 1.87.0/Cargo, schema C01 hasil codegen dari `9269c01`.
Fixture source/keputusan registry sintetis dari tes builder disimpan melalui
ArtifactStore nyata pada direktori temporer unik. Tidak ada pemanggilan model atau
perubahan corpus pengguna. Bootstrap diperiksa compile, tidak membuka listener.

| Pemeriksaan | Hasil aktual | Log / exit |
| --- | --- | --- |
| `cargo check -p regulagraph-ingestion --bin regulagraph-worker --offline` | PASS bootstrap ontology independen EXTRACT | `bootstrap.log`, 0 |
| `cargo test -p regulagraph-ingestion --lib --offline` | 179 PASS, 2 ignored opt-in; sebelum tambahan service test | `rust-lib.log`, 0 |
| `cargo test -p regulagraph-ingestion --lib assembly_worker --offline` | 4/4 PASS termasuk service test tambahan | `worker-service.log`, 0 |
| Reviewer: `cargo test -p regulagraph-ingestion --lib worker:: --offline` | 10/10 PASS sebelum service test tambahan | `independent-worker.log`, 0 |
| Reviewer: service ASSEMBLE tambahan | 1/1 PASS | `independent-service-elevated.log`, 0 |

Expected dan actual cocok: input valid menghasilkan delta yang bisa dibaca ulang
dengan hash terverifikasi, canonical entities dan source-backed assertion; checkpoint
mengikat ID/hash delta. Eksekusi ulang byte-identik. Role, auth scope, corpus, producer,
ID/hash plan, plan tidak ada, plan INDEX terselip, byte budget agregat dan korupsi
source ditolak. Cancellation sebelum mulai dan setelah pembacaan pertama gagal dengan
Cancelled. Runtime ontology yang tidak dikonfigurasi menghasilkan FailedPrecondition.

Service test mengubah request terbaru ke Prost dari bytes C01, memanggil metode Tonic
WorkerService dengan processor ASSEMBLE asli, memeriksa respons/checkpoint setelah
konversi balik, replay cache dan penolakan plan berbeda pada job yang sama. Tidak ada
socket/dua executable, sehingga jaringan end-to-end tidak dinyatakan lulus.

## Review dan kegagalan yang diperbaiki

Reviewer `verify_index_jobs` memeriksa input, resource, provenance, cancellation dan
batas authority secara independen; tidak ada blocker pada worker-only boundary.
Tambahan service test diperiksa dan dijalankan ulang setelah review pertama.

`worker.log` awal gagal compile karena fixture memakai lokasi tipe Lease dan akses
field error yang salah. `worker-retry.log` berikutnya gagal InvalidSource karena
fixture belum memperbarui fingerprint dependency setelah source benar-benar dipersist.
Fixture diperbaiki untuk memakai hash source aktual; validator tidak dilonggarkan.
`worker-fixed.log` dan run final lulus. Launch sandbox tertentu ditolak OS; rerun
elevated lulus dan log kegagalan tetap disimpan. Perubahan header mempertahankan gate
benchmark dan memperbarui status aktif, bukan menghapus panduan lama.

## Batas dan pekerjaan berikutnya

Worker tidak membuktikan registry receipt/checkpoint sumber durable atau live publisher
fence; coordinator Go wajib memverifikasinya sebelum admission dan commit. Plan/view
harus memakai layout storage content-addressed Rust. Cancellation tidak memutus satu
assembly sinkron di tengah; blob immutable dapat tersisa bila pembatalan terjadi
sesudah write. Blob tersebut belum published. ID checkpoint berdasarkan hash plan
tetap memerlukan binding job/attempt/fence oleh coordinator.

Persistence/inventory plan, dispatch dan admission output coordinator, Neo4j publication,
closure incremental serta benchmark masih terbuka. Tidak ada required latency/quality
yang diklaim tercapai; target tetap **REQUIRED_UNMEASURED**.
