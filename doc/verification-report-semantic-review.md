# Verifikasi review/resume resolusi operator lokal

Laporan ini mencatat perubahan setelah revision `fd878a0` pada 2026-10-09: workflow
inspeksi/acceptance, transaksi PostgreSQL review/intent/resume, migration 0017 dan
CLI operator. Fingerprint file dan raw log berada di
`artifacts/verification/20261009-semantic-review/results.json`.

## Cakupan dan hasil

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Workflow admission | Hash/revision/scope/corpus/producer pin/byte budget/reason salah tidak mencapai writer | PASS |
| Input binding | Mention, candidate, revision, alias support dan coverage yang diubah lalu di-hash ulang ditolak | PASS |
| DEFER PostgreSQL/executor | Proposal fixture diparkir, review diterima, intent dilanjutkan sampai STAGED tanpa model call baru | PASS |
| Storage failure/replay | Cancellation, revision berubah sebelum/sesudah review, changed replay, submit bersamaan, dan injected failure tidak meninggalkan acceptance parsial | PASS |
| LINK transaction | Actor/canonical/hash proposal cocok; review+intent+audit+resume rollback bersama; audit immutable dan exact replay | PASS |
| CLI | Explicit approval pins, actor argument ditolak, OS account tidak berubah oleh username env, backend error direduksi | PASS |
| Seluruh Go | `go test ./src/server/... -count=1` dengan PostgreSQL/Qdrant disposable | PASS |
| Static checks | `go vet ./src/server/...`, `git diff --check` | PASS |
| Review independen | Reviewer memeriksa diff dan menjalankan ulang target; temuan binding ditutup dengan regression | PASS untuk cakupan di atas |
| Model/legal quality dan required performance | Belum gold maupun workload profil release | NOT_MEASURED |

Perintah target adalah `go test ./src/server/internal/workflows ./src/server/cmd/cli
-run TestSemanticReview -count=1 -v`. Integration memakai PostgreSQL 16.8 disposable,
schema terpisah per kasus, serta source/model fixture deterministik. Pengujian seluruh
Go juga memakai Qdrant 1.18.0 disposable. Versi toolchain dan hash log dicatat pada JSON.
Tidak ada review terhadap corpus pengguna yang otomatis disetujui selama pengujian.

Log utama: `final-targeted.log`, `go-all.log`, `vet.log`; rerun independen
`independent-fixed.log` dan `independent-final-binding.log` (sesudah optimasi indeks
membership alias). Implementasi library tercatat pada `4d17b22`. Build awal sempat gagal karena nama field fixture/konstanta
error, dan injection rollback awal memakai nilai enum yang salah. Keduanya diperbaiki
sebelum hasil PASS; tidak dianggap kegagalan benchmark. Nilai enum pada injection final
diambil dari protobuf, bukan angka tebakan.

## Temuan dan batas bukti

Reviewer menemukan bahwa konsistensi internal hydrated item belum cukup untuk
membuktikan kesamaan dengan EXTRACT/candidate authoritative. Implementasi kini
membandingkan exact mention/candidate, expected revision, coverage lengkap, dan
alias support ownership. Lima regresi rehashed-input dan rerun independen lulus.
Pembacaan konteks asli tetap berasal dari trusted ingestion hydration dan audit
immutable; review tidak membaca ulang seluruh PDF/text sumber untuk setiap excerpt.

Fixture baru membuktikan DEFER melalui executor sampai registry/checkpoint. LINK
baru membuktikan transaksi review/intent/resume; jalur writer LINK/CAS mempunyai
suite PostgreSQL existing yang ikut full Go run. Ini belum satu run LINK nyata dari
model lokal sampai graph, belum multi-user RBAC, belum snapshot registry view,
CREATE/MERGE/SPLIT, ASSEMBLE atau Neo4j publication.

Identitas lokal berasal dari UID/SID akun OS dengan namespace host. Akses DB/config
adalah authority operator; tidak mengklaim bahwa OS username adalah role management.
Replay approval adalah acknowledgement historis, tidak membuktikan status registry
terkini dan tidak membatalkan cancellation. Approval baru tidak mengubah registry;
CAS tetap dilakukan executor di bawah lease/fence.

Target [benchmark](../configs/benchmark-targets.yaml) tidak berubah. Timing test,
build, schema dan fixture PASS bukan bukti throughput, latency p95/p99 produksi,
akurasi resolution atau kebenaran hukum. Deployment tetap di luar scope pengguna.
