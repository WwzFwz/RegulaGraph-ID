# Verifikasi handoff PARSE→STRUCTURE I01

Dokumen ini mencatat bukti penyelesaian handoff durable PARSE→STRUCTURE pada 2026-09-21. Perannya
mengikat kontrak artefak ternormalisasi, processor Rust, coordinator Go, retry per stage, dan recovery
checkpoint STRUCTURE ke revision yang dapat ditinjau. PASS dalam laporan ini hanya berlaku untuk
correctness serta durability scope tersebut; kualitas ekstraksi corpus dan target performa produksi tetap
**REQUIRED_UNMEASURED**.

## Identitas dan bukti

Audit dimulai dari commit `f69860f50fe59e6f87b7ba0bbb90b38154d917ce`. Fingerprint gabungan 41
file implementasi yang direview adalah
`834bc57674f68f5ac04021c7eabcdcc4fec76c335c5ae048bae9bba35f6bdbb2`. Raw log, source harness,
hash fixture/binary, patch, serta command exit code disimpan lokal dalam
`artifacts/verification/i01-structure-20260920/` dan diabaikan Git sesuai protokol verifikasi.

Target dan workload dalam `configs/benchmark-targets.yaml` tidak diubah. Tidak ada hasil correctness
fixture yang dilaporkan sebagai pencapaian latency, throughput, memory, atau akurasi corpus.

## Cakupan dan hasil

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Kontrak TextArtifact | PASS | Manifest normalizer mengikat software, versi, schema, konfigurasi, hash raw/normalized, MIME, dan schema descriptor |
| Processor STRUCTURE | PASS | Hanya menerima satu DocumentBatch PARSE lengkap, memverifikasi ulang raw/normalized/mapping, dan menghasilkan hierarchy tanpa ID hukum sintetis |
| Resource bound | PASS | Budget record global dihitung sebelum akumulasi structure lintas TextArtifact; limit per parser dijepit ke sisa budget |
| Handoff coordinator | PASS | Checkpoint PARSE mengikat input STRUCTURE; artifact output dan checkpoint diverifikasi sebelum state berubah |
| Retry per stage | PASS | Attempt/fence global tetap monotonik dan budget PARSE/STRUCTURE terpisah |
| Recovery STRUCTURE | PASS | Crash setelah checkpoint, attempt maksimum, serta error storage transient pulih tanpa mengulang worker atau menghabiskan budget tambahan |
| Ownership scheduler | PASS | Generic claim tidak merebut PARSE atau STRUCTURE yang masih dimiliki coordinator tahap dokumen |
| Cancellation abandoned | PASS | Queue/retry/staged tanpa lease dan RUNNING ber-lease kedaluwarsa berakhir CANCELLED |
| PostgreSQL aktual | PASS, 8 kasus | Termasuk fresh migration 0001–0003 dan replay idempotent |
| Native Go→Rust | PASS, 8 kasus | Valid hierarchy serta penolakan partial input, manifest palsu, MIME, dan schema descriptor |
| Workflow independen | PASS, 2 kasus | Recovery checkpoint dan backoff yang memakai stage attempt |
| Go | PASS, 62 top-level test | `go test ./src/server/...` dan `go vet ./src/server/...` |
| Rust | PASS, 97 unit + 1 integration | Satu fixture wire tetap ignored karena dijalankan oleh harness C01; Clippy juga PASS |
| Contract descriptor | PASS | 157 message, 31 enum, empat service; baseline tidak ditulis ulang |
| Benchmark produksi | NOT_MEASURED | Corpus acceptance, deployment referensi, dan workload produksi belum dijalankan |

Artifact store juga menjalankan 50 putaran concurrent identical writes dengan delapan writer per putaran.
Test tersebut membuktikan race direktori yang ditemukan audit tidak muncul kembali pada filesystem test;
hasilnya bukan benchmark throughput atau bukti durability filesystem produksi.

## Temuan yang ditutup

Audit independen menemukan dan memicu perbaikan pada nomor parameter SQL STRUCTURE, pemisahan retry
budget per stage, claim ownership, cancellation setelah owner mati, crash window antara checkpoint dan
completion, recovery pada attempt maksimum, error storage transient saat recovery, serta race pembuatan
shard artifact. Audit native juga menutup false success dari input PARSE parsial, provenance normalizer
yang sebelumnya terlalu longgar, descriptor MIME/schema yang dapat dipalsukan, dan akumulasi hierarchy
lintas dokumen yang sebelumnya baru dibatasi setelah seluruh alokasi selesai.

Verdict reviewer adalah **PASS untuk scope PARSE→STRUCTURE dan recovery checkpoint STRUCTURE**.

## Batas dan pekerjaan berikutnya

Terminal outcome PARSE belum disimpan secara durable bersama checkpoint. Karena checkpoint PARSE dapat
mewakili hasil lengkap maupun hasil parsial yang harus masuk `WAITING_REVIEW`, coordinator sengaja tidak
menganggap checkpoint PARSE sebagai bukti sukses. Crash sesudah checkpoint pada attempt terakhir dapat
berakhir `FAILED`; perbaikan berikut harus menyimpan completion outcome secara fenced/atomik atau menambah
kontrak recovery yang setara, lalu menguji kasus PDF blank/scan agar tidak pernah terpromosi ke `STAGED`.

Registry binding Regulation/Edition/Provision/ProvisionVersion, tahap CHUNK, graph/index, dan acceptance
benchmark belum termasuk verdict ini. Tahap berikutnya harus mempertahankan hierarchy sebagai output
tanpa identitas hukum, melakukan binding melalui registry authoritative, lalu baru membangun chunk yang
terikat provision version.
