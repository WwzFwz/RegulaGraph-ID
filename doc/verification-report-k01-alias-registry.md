# Laporan verifikasi K01: registry alias dan lookup kandidat

Dokumen ini mencatat pemeriksaan boundary PostgreSQL untuk registrasi alias bersumber dan lookup kandidat ambigu. Hasilnya **PASS untuk correctness yang diuji**, bukan kelulusan seluruh K01 atau release. Target kualitas hukum, latency p95/p99, throughput, dan resource dalam `configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**.

## Identitas dan bukti

Basis perubahan adalah commit `5f83a4e6fbbe243cd9b2d34e14bf3745cddcc9e3`; runtime Go 1.26.8 pada Windows amd64 dan PostgreSQL 18.0 lokal. Fingerprint SHA-256 final: migration 0008 `f0b28fff4268d3a93130413af858a76b9079fef78d737f064a5fe7e529247ebf`, writer `b22d73a2d8d0c616b42e411650ea456a255dd5e258244170798ba4092e2e0f5f`, reader `d9282e20f72ac84b4c8ed5561d5165091aa705baf04833ee5dca997209f957b4`, dan integration test `fc28d6ff2164af67125747cf557c1fa883b86a2bc47f5d6d7c7e6a55481de20f`. Raw output ada di `artifacts/verification/k01-alias-registry-20260924/` (diabaikan Git). Fixture tes dibuat langsung dari dua canonical organization pada database disposable `regulagraph_k01_v3`; tidak memakai gold regulasi.

| Pemeriksaan | Status dan hasil aktual |
| --- | --- |
| Migration dan PostgreSQL integration | **PASS**. `REGULAGRAPH_TEST_POSTGRES_DSN=... go test ./... -count=1` exit 0 dengan `GOCACHE` di `.cache/go-build-k01`; semua paket Go yang memiliki tes lulus. |
| Alias ambigu dan lookup kosong | **PASS**. Dua canonical ID tetap menjadi dua kandidat; lookup kosong memiliki revision 0 lalu berubah sesudah alias masuk. Batas hasil mengembalikan `ErrResultLimit`. |
| Revision, replay, dan idempotency | **PASS**. Operasi identik tidak menaikkan revision; payload replay berubah dan expected revision stale ditolak. |
| Integrity pada read/replay/reuse | **PASS**. Perubahan hash, surface, support refs, owner, lookup key, preferred label, serta orphan profile/alias ditolak; identitas tambahan pada profil tidak hilang. |
| Analisis statis | **PASS**. `go vet ./...` exit 0 dengan cache workspace. |
| Diff hygiene | **PASS**. `git -c core.safecrlf=false diff --check` exit 0. |
| Review independen | **PASS dalam scope integritas/idempotency**. Verifier terpisah menemukan celah hash-only pada read dan reuse, serta penerimaan status approved tanpa ledger; semuanya diperbaiki dan ia menjalankan ulang tes PostgreSQL. Tidak ada blocker tersisa dalam scope ini. |
| Kualitas model, legalitas support, dan performa | **NOT_MEASURED**. Tidak ada corpus/gold, workload referensi, atau run p95/p99 untuk klaim gate tersebut. |

Percobaan suite awal gagal karena default Go build cache berada di luar workspace yang dapat ditulis. Log `go-test.log` dan `go-vet.log` menyimpan kegagalan lingkungan tersebut; run ulang dengan cache workspace ada pada berkas berakhiran `-workspace-cache.log` dan lulus. Kegagalan awal tidak dihitung sebagai kegagalan implementasi.

## Batas hasil

Registry ini belum memiliki producer yang memverifikasi keberadaan `support_refs` pada artefak EXTRACT. Pemilihan legal scope/normalisasi, assignment RESOLVE, review approval, merge/split, dan lookup historical as-of juga belum dihubungkan. Migration 0008 belum direhearsal pada snapshot produksi. Semua itu tetap pekerjaan K01 sebelum kandidat dipakai untuk publication graph atau klaim akurasi entity resolution.
