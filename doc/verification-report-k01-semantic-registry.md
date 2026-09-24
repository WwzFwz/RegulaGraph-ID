# Verifikasi writer registry semantik K01

Dokumen ini merekam verifikasi submilestone writer PostgreSQL untuk keputusan LINK/DEFER pada
2026-09-24. Cakupannya adalah input EXTRACT dan kandidat yang terdaftar, review LINK yang
terikat persis, transaksi CAS satu revisi, ledger append-only, receipt, dan replay. Ini bukan
kelulusan stage RESOLVE, akurasi identitas hukum, atau release Hybrid GraphRAG.

Revision implementasi: `a286533` dengan regresi revokasi `79edae7` pada branch `main`. Fingerprint `git hash-object` migration
0009: `4a275f698b381dd5951aa876de4a5e40649353db`; writer:
`3a50252479bad4e420ea13da6964380f9dd477db`; input/review:
`3f13b2c007108ed213aa97ac62f7deaa1ee2e4f7`; replay:
`8a5483ba7015ecd7b43d7b9831ab100fa573be89`; tes integrasi:
`bd3dc1aa3719e9068ae3f850d56b99a8d2c4279f`. Log mentah dan versi toolchain berada
di `artifacts/verification/k01-semantic-registry-20260924/`. Lingkungan: Go 1.26.8
Windows/amd64 dan PostgreSQL 18 pada database disposable `regulagraph_k01g`, dimigrasikan
dari schema kosong. Fixture berisi satu canonical identity, satu alias, dua mention,
satu LINK yang direview, satu DEFER, serta lookup positif dan negatif; tidak memakai
corpus hukum/gold produksi.

| Pemeriksaan | Status | Expected dan hasil aktual |
| --- | --- | --- |
| Schema dan transaksi nyata | PASS | Migration 0009 diterapkan dari schema kosong; LINK+DEFER disimpan dalam satu revisi dengan keputusan unik per proposal. Tes menolak review hilang, direvokasi, proposal berubah dengan ID sama, kandidat lain, kandidat stale, dan artefak palsu. |
| CAS, retry, replay, integritas | PASS | Retry operation key yang sama menghasilkan keputusan sama; redecision dengan key baru ditolak; dua writer serentak menghasilkan tepat satu commit/revisi. Replay menolak hash keputusan yang sengaja dirusak setelah guard trigger dibypass dalam transaksi fixture. |
| Immutability | PASS | UPDATE/DELETE review, operation, dan decision ditolak trigger. Revokasi review satu arah dibedakan dari pengubahan isi. |
| Suite Go penuh | PASS | `go test ./... -count=1` dari `src/server`, exit 0, `go-test-final.log`. Tes PostgreSQL terarah diulang 20 kali, exit 0, `semantic-repeat-final.log`. Setelah regresi revokasi tambahan, tes terarah diulang dengan exit 0 pada `semantic-revocation-final.log`. |
| Pemeriksaan statis | PASS | `go vet ./...` dari `src/server`, exit 0, `go-vet-final.log`; `git diff --cached --check` exit 0 sebelum commit. |
| Review independen | PASS untuk scope writer | Agent `verify_k01_semantic_final` menemukan binding review yang kurang, race revokasi, serta ledger mutable. Implementasi dan tes diperbaiki; review ulang tidak menemukan blocker writer lokal. Agent membaca kode secara independen tetapi tidak menjalankan ulang PostgreSQL. |
| Go race detector | BLOCKED | `go test -race` gagal pada build `runtime/cgo`: GCC Cygwin tidak bisa membuat signal pipe (Win32 error 5); GCC MinGW terpasang hanya 32-bit. Kedua raw error ada di `semantic-race.log` dan `semantic-race-mingw.log`; ini bukan hasil assertion race. |
| Workflow RESOLVE dan acceptance | NOT_MEASURED | Caller belum mengambil byte melalui `ReadVerified` yang terikat checkpoint/fence dan belum ada producer review terautentikasi. Tes memasukkan review langsung via SQL. Kualitas false merge/split, latency p50/p95/p99, throughput, pool saturation, serta rollout migration pada salinan corpus produksi belum diukur. |

Adapter memverifikasi hash/metadata artefak dan membandingkan hasil lookup registry aktual sebelum
write, tetapi pendaftaran metadata saja belum membuktikan byte berasal dari checkpoint job yang
sah. Integrasi berikutnya harus mengikat `ReadVerified`, otorisasi review, fencing, dan
publication sebelum stage RESOLVE boleh dianggap aktif. Target numerik tetap dari
`configs/benchmark-targets.yaml` dengan status **REQUIRED_UNMEASURED**; tidak ada angka atau
workload yang diubah.
