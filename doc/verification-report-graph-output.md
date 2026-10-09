# Verifikasi dispatch dan admission GraphDelta

Laporan ini membuktikan boundary source-to-output ASSEMBLE pada library Go serta
kesesuaian canonical projection dengan worker Rust. Revision fixture `41c21e3`,
domain `d33ef2e`, workflow `9f24136`. Tanggal 2026-10-09; Go 1.26.8 windows/amd64,
Rust/Cargo 1.87.0. Agent `verify_index_jobs` melakukan review dan reproduksi independen.
Ini bukan completion K01, durable job commit, atau acceptance kualitas hukum.

## Perintah dan hasil

Raw logs: `artifacts/verification/20261009-graph-output/`. Fixture dibuat melalui
`src/ingestion/src/worker/assembly_tests.rs` pada revision fixture di atas; bytes
adalah keluaran processor/FileStore Rust aktual dengan input/registry sintetis.
Pilih path absolut berbeda pada `REGULAGRAPH_GRAPH_FIXTURE_DIR` untuk tiap ekspor.

| Pemeriksaan | Perintah / raw log | Actual |
| --- | --- | --- |
| Fixture dasar | `cargo test -p regulagraph-ingestion --lib assembly_worker_persists_deterministic_delta_and_bound_checkpoint --offline -- --nocapture`, `rust-fixture-final.log` | PASS, exit 0 |
| Fixture rich | `cargo test -p regulagraph-ingestion --lib assembly_worker_exports_rich_canonical_projection --offline -- --nocapture`, `rust-rich-final.log` | PASS, exit 0 |
| Go source projection + workflow, rich fixture | `go test ./src/server/internal/domain ./src/server/internal/workflows -run '^(TestGraphOutputFromRustWorker\|TestExecuteGraphAssemblyWithRustArtifacts)$' -count=1 -v`, `go-rich.log` | PASS, exit 0 |
| Seluruh Go, rich fixture env | `go test ./src/server/...`, `go-all.log` | PASS, exit 0; backend integration ber-env lain tidak dijalankan |
| Seluruh Rust library, tanpa env ekspor | `cargo test -p regulagraph-ingestion --lib --offline`, `rust-lib.log` | PASS, exit 0; 181 passed, 2 ignored |
| Go static checks | `go vet ./src/server/...`, `go-vet.log` | PASS, exit 0 |
| Reproduksi independen rich Rust dan Go | `independent-rich-rust.log`, `independent-rich-go.log` | PASS, exit 0; reviewer membuat fixture sendiri |

Tes Go khusus fixture skip bila env path tidak diisi. Skip bukan PASS cross-language;
run yang dilaporkan di atas mengisi env ke export Rust. Dua ignored Rust tests tetap
tidak diklaim lulus. Tidak ada server Neo4j atau PostgreSQL yang dilibatkan oleh suite ini.

## Expected versus actual

Delta sah harus cocok seluruhnya dengan source projection dan tidak mengubah input.
Fixture dasar dan rich diterima. Rich fixture membuktikan qualifier sort/dedup,
mention qualifier menjadi canonical, numeric negative zero, compare-date sort/dedup,
exception DAG remap serta dedup assertion/support. Source reference duplikat ditolak
validator EXTRACT; kasus itu tidak dipaksakan menjadi fixture positif.

Mutasi missing entity/mention/support/decision, reverse edge, predicate, invented source,
quote span, canonical ID, visibility, revision, dependency/negative lookup, extra closure,
report count dan unknown nested field ditolak. Source text hash salah, future context
schema, MIME salah, input melewati budget worker dan terlalu banyak entries teks kosong
juga ditolak. Hasil seluruh kasus sesuai expected.

Workflow memanggil port RPC satu kali setelah authority/source bytes sah, membatasi
deadline pada caller/pin/claim, serta mengulang authority setelah output validation.
Preflight gagal dan source corrupt tidak memanggil RPC. Stale response, checkpoint
manifest salah, unknown fields, checkpoint visibility, missing output/checkpoint,
late authority loss dan cancellation tidak mengembalikan VerifiedGraphOutput.
Accessor mengembalikan salinan. Physical artifact ID berbeda dari logical delta ID
diterima sesuai kontrak FileStore; kedua binding diuji terpisah.

## Temuan dan penutupan

Review meminta penyamaan budget input 16 MiB dengan worker, batas jumlah map teks
termasuk entries kosong, context schema1 serta MIME normalized text. Implementasi
memakai gate explicit dan helper budget bersama; regresi lulus. Envelope respons
diperketat terhadap unknown fields dan checkpoint visibility. Reviewer mengulangi
rich Rust-to-Go suite dan memberikan scoped PASS tanpa blocker tersisa.

Kegagalan awal berasal dari fixture export yang memakai logical ID pada descriptor
physical, fixture chunk tanpa token accounting, dan positive rich fixture dengan
source refs duplikat. Fixture diperbaiki sesuai kontrak; validator tidak dilonggarkan.
Log awal tetap disimpan. Pertanyaan review mengenai equality physical/logical output
ID ditarik setelah pemeriksaan worker; keduanya memang berbeda sesuai desain.

## Batas bukti dan langkah berikutnya

RPC/authority pada workflow tests adalah port sintetis yang mengirim bytes Rust nyata;
ini belum jaringan Go-to-Rust bersama registry PostgreSQL. Belum ada atomic output
registration/checkpoint/STAGED, reconciliation lost commit, daemon integration atau
publication Neo4j. Lanjutkan bagian tersebut tanpa menganggap VerifiedGraphOutput
sebagai bukti durable completion. Gold/model correctness, corpus penuh, latency,
throughput, memory dan semua required release gates tetap NOT_MEASURED.
