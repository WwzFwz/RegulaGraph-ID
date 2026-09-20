# Verifikasi handoff durable EXTRACT I01/K01

Dokumen ini merekam verifikasi boundary `CHUNK -> EXTRACT` dari claim PostgreSQL, dispatch worker Rust, pembacaan ulang artefak immutable, validasi proposal graph, hingga checkpoint dan recovery Go. Laporan ini membuktikan correctness yang diuji pada milestone ini; kualitas model, latency, throughput, biaya, dan acceptance produksi tetap **NOT_MEASURED/REQUIRED_UNMEASURED**.

## Cakupan implementasi

Coordinator merotasi claim PARSE, STRUCTURE, CHUNK, dan EXTRACT tanpa membiarkan claim generik mengambil stage milik executor khusus. EXTRACT hanya menerima satu checkpoint CHUNK terminal sukses. Output dibaca ulang melalui descriptor content-addressed sebelum registrasi artefak, dependency manifest, dan checkpoint fenced.

Validator menolak perbedaan corpus/config/source, media/schema yang salah, `DocumentBatch` parsial atau tanpa chunk, accounting yang tidak sama dengan populasi chunk, model/prompt di luar config ingestion, qualifier canonical prematur, endpoint/support/evidence asing, issue rejection yang tidak menunjuk chunk, dan provenance source/version yang tidak menutup setiap span. Teks ternormalisasi yang benar-benar dipakai evidence dihidrasi dengan aggregate byte cap; mention harus sama persis dengan byte sumber dan semua span harus berada pada boundary UTF-8. Error baca storage sementara tetap retryable, sedangkan korupsi kontrak menjadi terminal.

Checkpoint EXTRACT sukses maupun gagal dapat dipulihkan pada fence baru tanpa memanggil worker/model kembali. Recovery mempertahankan terminal outcome, dependency, artefak, dan state tujuan. Jalur ini menutup crash window sesudah checkpoint; crash sebelum checkpoint masih mengikuti idempotensi worker dan belum dibuktikan dengan proses lintas runtime nyata.

## Hasil verifikasi

| Pemeriksaan | Hasil | Batas bukti |
| --- | --- | --- |
| Go test seluruh module server | PASS | 157 test events PASS dan 5 skip; fixture/double deterministik, test PostgreSQL yang memerlukan DSN dapat skip |
| Go vet seluruh module server | PASS | Static analysis Go |
| Adversarial verifier | PASS setelah perbaikan | 25 kasus: accounting, source partial/MIME, reverse evidence, external model pin, producer-config separation, forged mention, UTF-8 support, transient hydration, dan recovery sukses/gagal |
| Claim SQL EXTRACT | PASS static review | Belum dijalankan terhadap PostgreSQL aktual pada audit ini |
| Rust -> Semantic Gateway -> Go -> PostgreSQL | NOT_MEASURED | Belum ada run proses nyata lintas runtime/provider |
| Quality dan performance required | NOT_MEASURED | Gold extraction set, provider/model produksi, workload, dan hardware referensi belum dibekukan |

Raw evidence verifier berada di `artifacts/verification/i01-durable-extract-20260921/` dan sengaja tidak menjadi klaim benchmark produksi. Fingerprint source final yang ditinjau adalah `b5816983e25e4991a4f6ee198b3121a8c6f1505ff0f409059e73573979231124`.

## Pekerjaan berikutnya

Tahap berikutnya adalah ontology enforcement versioned di sisi extraction, lalu RESOLVE dengan blocking, canonical revision, keputusan merge/split reversible, dan review path. Sebelum profil release, jalankan PostgreSQL aktual, proses lintas runtime lengkap, failure injection sebelum/sesudah checkpoint, race detector pada toolchain yang didukung, serta benchmark kualitas/latency/throughput sesuai `configs/benchmark-targets.yaml`.
