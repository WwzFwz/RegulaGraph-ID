# Laporan verifikasi K01: stage handoff BIND dan CHUNK

Dokumen ini mencatat verifikasi independen untuk kontrak wire, migration, semantic stage ordering, serta primitive claim/checkpoint PostgreSQL BIND dan CHUNK. Statusnya **PASS untuk submilestone stage handoff**. Executor BIND, RPC CHUNK aktual, dan benchmark produksi berada di submilestone terpisah; target numerik tetap **REQUIRED_UNMEASURED**.

## Bukti dan hasil

Fingerprint file perilaku yang diaudit adalah `65414318b79406a09de200cc0859e5e2795b2f0e919df76fc7f2cd82e15437b8`. Raw log, fixture, failure injection, dan hash disimpan di `artifacts/verification/k01-stage-handoff-20260921/`; direktori ini diabaikan Git.

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Compatibility schema | PASS | 157 message, 31 enum, empat service; BIND=8 dan CHUNK=9 additive, tanpa perubahan tag/type lama |
| Semantic stage order | PASS | Rank eksplisit menempatkan STRUCTURE→BIND→CHUNK→EXTRACT meski nomor enum append-only tidak berurutan secara eksekusi |
| Claim isolation | PASS | 15 leaf case termasuk matriks 24 kombinasi stage/state; generic claimant tidak mengambil stage yang dimiliki coordinator khusus |
| Retry dan recovery | PASS | Due retry, attempt budget, terminal checkpoint recovery, fence, dan stage handoff diuji untuk BIND/CHUNK |
| PostgreSQL aktual | PASS, 2/2 suite | Fresh migration, upgrade 0005→0006, replay checksum, dan integration repository |
| Failure injection migration | PASS setelah perbaikan | Kegagalan ledger menggulung kembali constraint DDL; migration tidak memiliki transaction control internal |
| Go | PASS | 109 test event dan `go vet ./src/server/...` |
| Rust | PASS | 99 unit, satu native PDFium, satu ignored wire fixture; Clippy lulus |
| Benchmark produksi | NOT_MEASURED | Queue latency, claim throughput, lock contention, dan workload corpus belum diukur |

## Keputusan semantik

Nilai enum lama EXTRACT–INDEX tidak dinomori ulang karena itu akan merusak wire compatibility. BIND dan CHUNK ditambahkan pada nilai 8/9, sedangkan `JobStageRank` menjadi satu-satunya pemilik urutan eksekusi. PostgreSQL menyimpan nilai wire, tetapi pemeriksaan regresi checkpoint memakai rank tersebut.

Migration 0006 hanya memperluas check constraint job dan checkpoint. Transaction serta pencatatan checksum dimiliki migration runner dalam satu transaksi. Versi awal migration sempat memakai `BEGIN/COMMIT` internal; failure injection membuktikan DDL dapat terlepas dari ledger, sehingga transaction control tersebut dihapus sebelum commit.

## Pekerjaan berikutnya

Primitive ini belum membuktikan bahwa isi document binding benar atau worker CHUNK sudah memproses batch. Executor BIND harus diuji bersama registry dan artifact store, lalu worker CHUNK harus menerima tepat satu batch registry-bound, mempertahankan provision-version per node, dan menulis terminal checkpoint. Lease heartbeat dan benchmark PostgreSQL tetap belum tersedia.
