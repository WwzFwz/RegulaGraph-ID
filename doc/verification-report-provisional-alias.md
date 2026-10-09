# Verifikasi bootstrap identity provisional

Laporan ini mencatat verifikasi jalur operator EXTRACT occurrence menuju inventory
canonical provisional pada 2026-10-09, baseline `97dc632`. Cakupannya adalah
identity-key, source authority, CAS/replay, atomic storage, candidate hydration,
dan publication-bound canonical export. Ini bukan acceptance semantic CREATE,
model extraction, legal correctness, atau keseluruhan GraphRAG.

## Bukti aktual

Toolchain: Go 1.26.8 Windows/amd64, PostgreSQL lokal. Tes database memakai schema
`review_<timestamp>` yang dibuat dan dibersihkan sendiri oleh `reviewDatabase`;
schema corpus operasional tidak dimigrasikan atau dihapus. EXTRACT sintetis,
sedangkan allocator BIND, FileStore, evidence catalog, registry writer/reader,
candidate hydration dan export memakai implementasi produksi.

| Pemeriksaan | Expected / actual | Status |
| --- | --- | --- |
| Namespace v1 | Vektor ID tetap; model/mention/chunk/subset SourceRefs tidak mengganti ID | PASS |
| Perubahan anchor | Parser/normalizer/teks/span/tipe/scope mengganti ID dan approval | PASS |
| Boundary tipe | Regulation, organization, provision serta caller-supplied target ditolak | PASS |
| Atomic creation | Identity/profile/alias/lookup/review muncul pada E+1; late review failure rollback | PASS |
| Revision/concurrency | Revision stale ditolak; dua operasi bersaing menghasilkan satu commit | PASS |
| Lookup negatif | Kandidat pada scope kedua dan metadata lookup incomplete menolak commit | PASS |
| Restart replay | Inspect historis dan accept identik memulihkan hasil tanpa identity baru; actor berubah ditolak | PASS |
| Konsumen | Candidate batch/hydration dan canonical export mempertahankan ID, bukti, namespace dan UNREVIEWED | PASS |
| Existing alias | Delapan skenario existing-target tetap lulus setelah perubahan transaksi | PASS |
| CLI | Mode eksplisit, no target/canonical, approval pins dan error lama | PASS |
| Package regression / vet | Empat package terdampak lulus; tes DB pada run umum SKIP, terpisah dari run DB khusus | PASS |
| C01 | 173 messages, 32 enums, 4 services; baseline tidak ditulis ulang | PASS |
| Durable LINK sampai Rust ASSEMBLE pada fixture baru | Belum dijalankan sebagai satu rantai | NOT_MEASURED |
| Model/gold/required performance | Tidak ada run acceptance | NOT_MEASURED |

Perintah aktual, seluruhnya exit 0 pada run akhir:

```text
go test ./src/server/internal/workflows -run 'Test(ProvisionalAlias|SourcedAlias)' -count=1 -v
go test ./src/server/internal/workflows ./src/server/cmd/cli -run 'TestProvisionalAlias(SourceIdentity|Command)' -count=1 -v
go test ./src/server/internal/domain ./src/server/internal/adapters/postgres ./src/server/internal/workflows ./src/server/cmd/cli -count=1
go vet ./src/server/internal/domain ./src/server/internal/adapters/postgres ./src/server/internal/workflows ./src/server/cmd/cli
python scripts/check_contracts.py
```

Raw log dan fingerprint disimpan di
`artifacts/verification/20261009-provisional-alias/` (diabaikan Git):
`integration-final.log`, `packages.log`, `vet.log`, dan `manifest.json`.
Run awal anchor-subset gagal karena fixture occurrence tidak berada dalam dua
provision; fixture diperbaiki supaya benar-benar menguji subset sumber berbeda,
lalu seluruh regression dijalankan ulang. Tidak ada target yang diturunkan.

## Review independen dan pekerjaan lanjutan

Agent `verify_index_abort` memeriksa perubahan secara read-only, menjalankan
unit/CLI sendiri, serta memeriksa tes/log PostgreSQL implementer dan export
publication-bound. Verdict akhir: **PASS terbatas untuk bootstrap provisional,
tanpa blocker dalam scope**. PostgreSQL di sesi reviewer SKIP karena DSN tidak
tersedia; PASS DB berasal dari run implementer, bukan klaim reviewer menjalankannya.

Review meminta regresi namespace/stabilitas/tipe/CLI yang telah ditambahkan.
Batas berikut tetap terbuka: alias baru untuk provisional existing, semantic
CREATE/MERGE/SPLIT penuh, dan run durable LINK sampai ASSEMBLE dari fixture ini.
[Panduan operasional](provisional-entity-review.md) menjelaskan batas-batas tersebut.
Migrasi 0026 diuji pada schema terisolasi; belum diterapkan ke corpus pengguna.
