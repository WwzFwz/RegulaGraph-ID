# Laporan verifikasi K01: exact canonical registry

Dokumen ini mencatat bukti verifikasi independen untuk planner exact regulation identity dan allocator canonical identity PostgreSQL K01. Status implementasi yang diuji adalah **PASS untuk correctness dalam cakupan K01 exact registry**; hasil ini bukan kelulusan resolusi identitas semantik, akurasi hukum, atau benchmark produksi. Target numerik pada `configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**.

## Cakupan dan identitas bukti

Verifikasi dijalankan dari base commit `d2fdd1cf18af51cefc6c2c188dc7c8e2eea55878`. Fingerprint final lima file kode dan migration adalah `b31bea6eec36634b36d4d3b42f678c91f99cf5d7e2b3e1e4a03137c6b4936406`. Perubahan dari fingerprint perilaku yang diuji hanya dua baris komentar header pada integration test dan telah diaudit byte-for-byte. Raw log, overlay audit, fixture, delta audit, dan machine-readable verdict disimpan di `artifacts/verification/k01-exact-registry-20260921/`; direktori tersebut merupakan artefak lokal yang diabaikan Git.

| Pemeriksaan | Hasil | Bukti utama |
| --- | --- | --- |
| Planner domain independen | PASS, 15/15 kasus | Determinisme, batas input, duplicate/foreign observation, status observation, metadata hilang/konflik, serta stale derived key |
| Pencegahan false merge exact | PASS | Perbedaan issuer, jenis, nomor, tahun, atau jurisdiction menghasilkan identity key berbeda |
| Review dan provenance | PASS | Assertion tidak lengkap, konflik nilai, atau jurisdiction sumber yang berlawanan dengan policy diarahkan ke review |
| Mutation safety | PASS | Candidate yang dimutasi setelah planning ditolak saat claim dibentuk ulang |
| Migration 0005 | PASS pada PostgreSQL 18 | Fresh apply, checksum replay, upgrade valid dari 0004, natural key tekstual lama, serta rollback atomik pada orphan |
| Allocation dan replay | PASS | Pembuatan ID opaque, exact-key linking, operation claim replay, dan konflik payload/key terdeteksi |
| Audit corruption | PASS, 6 kasus | Drift canonical row, history hilang/parsial, revision dan interval invalid ditolak oleh constraint atau persistent-integrity guard |
| Concurrency | PASS | Delapan caller konkuren dengan exact key sama menghasilkan satu canonical ID dan tepat satu assignment `Created=true` setelah retry caller untuk SQLSTATE `40001` |
| Revision semantics | PASS | Batch creation menaikkan corpus revision sekali; link ke key lama tidak menaikkan revision; expected revision stale ditolak |
| Full Go suite | PASS | 87 test event lulus; tiga skip tercatat untuk native worker env, wire fixture env, dan privilege symlink Windows |
| Static analysis | PASS | `go vet ./src/server/...` |
| Benchmark produksi | NOT_MEASURED | Tidak ada klaim latency, throughput, RSS, false merge, atau false split terhadap corpus/gold set produksi |

## Semantik yang dibuktikan

Planner memperlakukan metadata portal sebagai assertion sumber, bukan kebenaran legal final. Hanya observation lengkap dan berhasil, dengan issuer/type/number/year/title yang konsisten serta jurisdiction policy eksplisit, yang menjadi exact identity candidate. Exact key mengikat tuple yang dinormalisasi; planner tidak menebak tanggal hukum, issuer canonical, atau kesamaan semantik.

Allocator memakai transaksi serializable dan corpus revision lock. Operation key mengikat hash dari kumpulan claim yang terurut, sehingga replay identik mengembalikan assignment historis dan replay yang berubah ditolak. Canonical row yang dipakai ulang harus cocok dengan scope, key, tipe, revision, serta interval validitas half-open; payload pada decision history harus cocok dengan replay claim. PostgreSQL dapat mengembalikan SQLSTATE `40001`; caller harus mengulang operasi yang sama dengan operation key dan claim identik menggunakan budget retry terbatas.

Migration 0005 mengisi keputusan lama dari canonical row yang dirujuk dan menggagalkan upgrade bila menemukan orphan. Operation header sintetis untuk history lama memakai fingerprint payload yang tersedia hanya sebagai jejak audit. Header tersebut tidak membuktikan claim request historis dan tidak diperlakukan sebagai replay API modern.

## Batas hasil dan pekerjaan lanjutan

K01 exact registry belum membuat atau mengikat record `Regulation`, `Edition`, `Provision`, dan `ProvisionVersion`. API/gRPC registry, stable issuer identity resolution, fuzzy/semantic merge-split, human review workflow, serta gold-set accuracy belum tersedia. Rust/native inference dan codegen tidak berubah dalam scope ini. Tahap berikutnya mengubah assignment exact menjadi record regulasi berversi dengan provenance lengkap, lalu menghubungkannya ke CHUNK dan change-event tanpa melewati keputusan registry revisioned.
