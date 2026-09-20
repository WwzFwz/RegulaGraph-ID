# Laporan verifikasi K01: executor BIND durable

Dokumen ini mencatat verifikasi independen untuk executor BIND Go dari artefak STRUCTURE hingga handoff CHUNK. Statusnya **PASS untuk correctness milestone BIND durable**. Exact canonical binding, output immutable, dependency evidence, retry/recovery, cancellation, dan migration PostgreSQL sudah dibuktikan pada scope ini. Kualitas resolusi semantik, CHUNK produksi, serta target numerik performa tetap **REQUIRED_UNMEASURED**.

## Bukti dan hasil

Fingerprint 14 file perilaku yang diaudit adalah `1f1d2c9aed3a50a1b261436abd35ab248788e71c8cc78b7e54857746f9fb91af`. Raw log adversarial, PostgreSQL, migration, full Go, dan vet disimpan di `artifacts/verification/k01-bind-executor-20260921/`; direktori tersebut diabaikan Git. Audit akhir menjalankan 34 leaf case independen tanpa temuan terbuka.

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Jalur lengkap dan parsial | PASS | PostgreSQL aktual dan FileStore aktual membuktikan COMPLETE→STAGED serta PARTIAL→WAITING_REVIEW |
| Registry skala besar | PASS | 10.001 provision claim dibagi menjadi batch deterministik/idempotent; batas adapter tidak berubah menjadi retry identik |
| Crash/replay | PASS, 10 titik | Response loss pada tiga phase registry, register artefak, dependency, checkpoint sebelum/sesudah, completion sebelum/sesudah, dan recovery checkpoint |
| Perubahan konfigurasi | PASS | Ukuran partition dan yurisdiksi berubah saat restart tanpa operation-key collision; producer manifest mengikat konfigurasi semantik |
| Recovery payload | PASS | Bytes output dibaca ulang dan divalidasi terhadap hash, media type, schema, corpus, producer, completeness, terminal outcome, fence, dan cancellation |
| FileStore | PASS, 6 kasus adversarial | Growth setelah stat, perubahan setelah hash pertama, cap, missing parent, cancellation, dan traversal ditolak dengan klasifikasi tepat |
| Migration 0007 | PASS | Upgrade 0006→0007, replay checksum, legacy row, constraint fingerprint/empty-result, serta injected ledger failure rollback pada PostgreSQL aktual |
| Scheduler fairness | PASS | Preferensi PARSE/STRUCTURE bergantian di dalam executor dan preferensi document/BIND bergantian di daemon |
| Full Go dan vet | PASS | 119 test event lulus dengan DSN PostgreSQL aktif; tiga skip yang tercatat adalah native worker interop, wire fixture interop, dan privilege Windows leaf symlink; PostgreSQL 2/2 serta `go vet ./...` lulus |
| Benchmark produksi | NOT_MEASURED | Queue p95/p99, registry contention, throughput corpus, peak RSS, dan target kualitas belum diukur |

## Perbaikan hasil audit

Audit awal menemukan recovery error yang tidak menutup attempt, output recovery yang hanya memeriksa metadata, hashing file tanpa batas sebelum alokasi, error artefak deterministik yang dianggap transient, claim lebih dari 10.000 yang diulang identik, operation key yang tidak mengikat partition/config, starvation STRUCTURE, dan dependency row tanpa fingerprint aktual. Seluruh counterexample direproduksi, diperbaiki, lalu lulus pada rerun gabungan.

Executor kini mengurutkan claim secara global, membaginya dalam batch maksimum 10.000, dan membentuk operation key dari job, input, phase, ukuran partition, index, serta fingerprint isi claim. Crash di tengah phase mereplay batch lama tanpa canonical ID ganda. Output baru didaftarkan bersama fingerprint dependency dan empty lookup sebelum terminal checkpoint, sehingga checkpoint tidak dapat menutup handoff yang dependency evidence-nya belum durable.

FileStore memeriksa ukuran sebelum hashing, membatasi stream pada ukuran referensi ditambah satu byte, lalu memverifikasi bytes kembali setelah bounded allocation. Missing path, mismatch, traversal, symlink, dan pelanggaran cap ditandai sebagai integritas persisten; error availability tetap mengikuti retry budget.

## Pekerjaan berikutnya

Milestone ini belum menjalankan CHUNK sebagai stage durable. Tahap berikutnya adalah menghubungkan Rust parent-aware chunk builder ke claim CHUNK, membaca tepat satu output BIND terverifikasi, mempertahankan provision-version per node, menyimpan artefak/checkpoint fenced, serta membuktikan recovery dan handoff menuju EXTRACT. Lease heartbeat dan benchmark corpus-scale tetap diperlukan sebelum profil release dapat dinyatakan lulus.
