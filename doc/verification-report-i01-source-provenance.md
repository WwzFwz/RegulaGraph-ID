# Verifikasi handoff source provenance I01

Dokumen ini mencatat verifikasi handoff metadata acquisition ke PARSE/STRUCTURE pada 2026-09-21. Perannya membuktikan bahwa assertion portal dibawa bersama identitas corpus dan hash PDF tanpa dipromosikan menjadi identitas regulasi canonical. Verdict PASS hanya berlaku untuk correctness dan integritas boundary ini; target kualitas hukum, latency, throughput, memori, dan biaya tetap **REQUIRED_UNMEASURED**.

## Identitas dan bukti

Audit dimulai dari commit `99416ce6ea9a25c8b1414a2392640d83738f62de`. Fingerprint gabungan 15 file yang direview adalah `66d65cb7fa2f951f8e1dcfe73e91d393a478b21db9709e0ee13fccf64a65a186`. Raw log, harness adversarial, hasil kompatibilitas corpus, toolchain, dan exit code disimpan lokal dalam `artifacts/verification/i01-source-provenance-20260921/` dan diabaikan Git sesuai protokol verifikasi.

Target dan workload dalam `configs/benchmark-targets.yaml` tidak diubah. Correctness fixture dan pemrosesan inventory lokal tidak dilaporkan sebagai pencapaian benchmark produksi.

## Cakupan dan hasil

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Kontrak wire | PASS | `IngestionRequest` dan `ProcessBatchRequest` dapat membawa `SourceObservation`; descriptor tetap 157 message, 31 enum, dan empat service |
| Handoff acquisition | PASS, 9/9 kasus adversarial | ID, urutan, canonical metadata, dan hash metadata deterministik; portal/URL, receipt, duplicate ID, serta descriptor content yang bertentangan ditolak |
| Kompatibilitas corpus lokal | PASS | 595 record berstatus complete diproyeksikan menjadi 639 observation tanpa error |
| Binding coordinator Go | PASS, 6/6 kasus | Request valid dan legacy diterima; foreign corpus, blob, portal, dan duplicate observation ditolak sebelum dispatch |
| Preflight worker Rust | PASS, empat kasus forged | Corpus, duplicate ID/artifact, dan source-blob di luar request ditolak sebelum parsing atau penulisan artefak |
| Retention PARSE→STRUCTURE | PASS | `SourceObservation` bertahan identik pada `DocumentBatch` PARSE dan STRUCTURE; request legacy tanpa observation tetap kompatibel |
| Go | PASS | `go test ./src/server/...` dan `go vet ./src/server/...` |
| Rust | PASS, 97 unit + 1 native | Satu fixture wire tetap ignored sesuai suite induk; Clippy PASS |
| Python unit pendukung | PASS, 51 test | Evaluator/tooling dan generated binding; tidak membuktikan kualitas model |
| Benchmark produksi | NOT_MEASURED | Corpus-scale concurrency, latency, memory, kualitas identitas hukum, dan publication acceptance belum dijalankan |

Verdict reviewer adalah **PASS untuk source provenance acquisition→PARSE→STRUCTURE** tanpa temuan kode terbuka. Tujuh counterexample audit awal—descriptor hash sama yang bertentangan, hash metadata bergantung urutan input, deduplikasi setelah trim tidak stabil, hash tidak mengikat seluruh output, urutan timestamp ambigu, resolved URL asing, dan validasi worker setelah I/O—telah direproduksi, diperbaiki, dan diuji ulang.

## Semantik keamanan dan integrasi

`handoff.go` hanya menerima portal resmi yang dikonfigurasi, receipt PDF complete, path content-addressed, ukuran positif, dan timestamp valid. Satu hash PDF menghasilkan satu source locator; beberapa pengamatan portal atas bytes yang sama tetap dipertahankan sebagai observation terpisah. Hash metadata dihitung dari seluruh `portal_metadata` canonical yang benar-benar dikirim, termasuk kind, label, dan URL receipt.

Coordinator memeriksa hubungan corpus, portal, dan source-blob sebelum memanggil worker. Worker mengulang pemeriksaan terhadap source hash yang dideklarasikan sebelum pekerjaan PDFium atau persistence. STRUCTURE meneruskan observation dari batch immutable sehingga registry berikutnya dapat membedakan metadata sumber dari keputusan canonical.

## Batas dan pekerjaan berikutnya

Builder mempercayai receipt acquisition yang sudah diverifikasi dan tidak membaca ulang atau meng-hash ulang file PDF; verifikasi bytes aktual tetap dilakukan oleh worker saat membuka descriptor. Collector juga belum otomatis mengirim record complete menjadi durable ingestion job. Tahap berikutnya adalah adapter submit/replay inventory, registry binding `Regulation`, `DocumentEdition`, `Provision`, dan `ProvisionVersion`, kemudian CHUNK yang terikat provision version. Metadata yang tidak cukup atau ambigu harus masuk review dan tidak boleh diisi melalui tebakan nama yang mirip.
