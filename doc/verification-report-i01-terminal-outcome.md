# Verifikasi terminal outcome checkpoint I01

Dokumen ini mencatat verifikasi recovery durable PARSE dan STRUCTURE pada 2026-09-21. Perannya mengikat kontrak `Checkpoint.terminal_status`, producer Rust, migration PostgreSQL, validator Go, dan coordinator recovery ke bukti yang dapat ditinjau. Verdict PASS hanya berlaku untuk correctness dan durability pada scope ini; target kualitas corpus, latency, throughput, dan memori tetap **REQUIRED_UNMEASURED**.

## Identitas dan bukti

Audit dimulai dari commit `b3697654409c1bc205e936d902be0389d9227c13`. Fingerprint gabungan 20 file yang direview adalah `b7dc11b14e88d7d27398ec01af8fd40855331413e75ef34cd449584d8f10d6c7`. Raw log, source harness, fixture hash, patch, toolchain, dan exit code disimpan lokal dalam `artifacts/verification/i01-terminal-outcome-20260921/` dan diabaikan Git sesuai protokol verifikasi.

Target dan workload dalam `configs/benchmark-targets.yaml` tidak diubah. Correctness fixture tidak dilaporkan sebagai pencapaian benchmark produksi.

## Cakupan dan hasil

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Kontrak checkpoint | PASS | Response worker baru wajib menyamakan terminal outcome checkpoint dan response; status unspecified atau forged ditolak |
| Producer Rust | PASS | PARSE lengkap menyimpan `SUCCEEDED`, hasil parsial menyimpan `FAILED`, dan STRUCTURE menyimpan `SUCCEEDED` |
| Persistence PostgreSQL | PASS | Payload dan kolom outcome disimpan bersama; mismatch dibaca sebagai persistent-integrity error |
| Recovery PARSE | PASS | Outcome lengkap kembali ke `STAGED`; parsial kembali ke `WAITING_REVIEW` tanpa menjalankan worker ulang |
| Recovery STRUCTURE | PASS | Recovery sukses sebelumnya tetap berlaku tanpa menghabiskan budget stage tambahan |
| Attempt maksimum | PASS | Checkpoint baru tetap recovery-eligible sementara stage attempt tidak bertambah |
| Kompatibilitas legacy | PASS | Outcome `NULL` tidak diinfer sebagai sukses; job legacy yang exhausted berakhir `FAILED` |
| Failure injection | PASS | Outage metadata dan penyimpanan checkpoint transient dapat dicoba ulang tanpa kehilangan outcome |
| PostgreSQL/native | PASS, 14 kasus | Fresh migration 0001–0004, replay, checksum drift, PARSE lengkap/parsial, STRUCTURE, cancellation, dan ownership |
| Matriks outcome | PASS, 15 kasus | Semua kombinasi response `SUCCEEDED`/`FAILED`/`CANCELLED` dengan checkpoint valid, unspecified, berbeda, dan unknown |
| Go | PASS, 64 top-level test | `go test ./src/server/...` dan `go vet ./src/server/...` dengan PostgreSQL/native aktif |
| Rust | PASS, 97 unit + 1 integration | Satu fixture wire tetap ignored karena dijalankan oleh harness C01; Clippy dan worker build juga PASS |
| Contract descriptor | PASS | 157 message, 31 enum, empat service; baseline tidak ditulis ulang |
| Python unit pendukung | PASS, 51 test | Loader/evaluator/tooling dengan generated bindings; tidak membuktikan kualitas model |
| Benchmark produksi | NOT_MEASURED | Corpus-scale concurrency, latency, memory, kualitas hukum, dan publication acceptance belum dijalankan |

Verdict reviewer adalah **PASS untuk terminal outcome durable serta recovery checkpoint PARSE/STRUCTURE** tanpa temuan kode terbuka.

## Kompatibilitas dan rollout

Migration 0004 menambah kolom nullable agar checkpoint lama tetap dapat dibaca. Nilai `NULL` hanya berarti legacy atau outcome tidak diketahui; nilai tersebut tidak membuka jalur recovery pada attempt maksimum dan tidak boleh dipromosikan sebagai sukses. Job legacy yang budgetnya sudah habis dapat memerlukan reprocessing eksplisit.

Rollout perlu menerapkan migration 0004 sebelum producer Rust baru menulis outcome, lalu memasangkan worker dan coordinator yang memakai kontrak baru dalam compatibility window yang sama. Coordinator baru membaca row legacy secara fail-safe. Rollback aplikasi tidak menghapus kolom atau mengedit checksum migration yang sudah tercatat.

## Batas dan pekerjaan berikutnya

Windows leaf-symlink dan fixture wire tertentu tetap skipped/ignored sesuai suite induknya. Belum ada bukti corpus-scale, contention, long-running lease heartbeat, atau benchmark numerik. Tahap berikutnya tetap registry binding `Regulation`, `DocumentEdition`, `Provision`, dan `ProvisionVersion`, lalu CHUNK yang terikat provision version; scheduler perlu lease heartbeat sebelum batch panjang.
