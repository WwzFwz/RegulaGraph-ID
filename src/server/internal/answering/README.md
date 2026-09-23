# src/server/internal/answering

Penyusunan konteks, pembuatan jawaban, dan pemeriksaan citation menggunakan bukti hasil retrieval. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak menyimpan fakta baru ke graph atau menganggap kelancaran bahasa sebagai bukti kebenaran. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mempertahankan hubungan claim-evidence, versi pasal, dan cakupan corpus. Bukti tidak cukup atau bertentangan harus dapat menghasilkan jawaban terbatas; pengecekan referensi berbeda dari pengecekan dukungan semantik.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [citations.go](citations.go), [context_builder.go](context_builder.go), [generator.go](generator.go), [validation.go](validation.go).

## Benchmark dan perhatian performa

**CONTEXT.** Ukur cakupan gold evidence, kelengkapan jalur graph, duplikasi, token count, dan waktu membangun konteks. Gate: tiap item konteks dapat dipetakan ke sumber dan versi; pemotongan/ketidakcukupan bukti dilaporkan. Context budget tidak boleh diam-diam menghapus syarat penting.

**ANSWER.** Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector/audit D01, kontrak/validator C01, evaluator E01, serta fondasi storage/publication S01 sudah tersedia. Pipeline parsing/graph/retrieval, mutasi backend, layanan model, gold dataset, dan acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing; audit integrity, build, dan fixture tidak membuktikan target kualitas atau latency.

[context_builder.go](context_builder.go) kini mengepak bukti langsung dari satu snapshot dalam urutan retrieval, menghitung seluruh fragmen melalui penghitung tokenizer yang dipasok caller, serta menandai item, parent, path, dan dependency yang tidak masuk sebagai konteks parsial. ID jalur graph tidak dianggap bukti jalur sudah dirender. Parent/pengecualian/path hydration dan tokenizer generator aktual belum tersambung; generator, citation mapper, dan validasi semantik jawaban tetap scaffold.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [citations.go](citations.go) | Build citations from trusted snapshot-bound source metadata and evidence locators, never model-invented URLs. | Call VerifyCitationEvidence with trusted lookup; test forged URLs, wrong versions/pages/spans and unsupported claim mappings. |
| [context_builder.go](context_builder.go) | Pack ordered evidence with parent context using the exact generator tokenizer; preserve exceptions and required path sets. | Test oversized clauses, repeated parents and insufficient token budget; record omitted required evidence and actual token count. |
| [generator.go](generator.go) | Generate claims constrained to selected evidence, explicit partial/abstain/conflict states and provisional stream events. | Evaluate faithfulness and answer correctness independently; measure TTFT/completion/cost and test interrupted generation. |
| [validation.go](validation.go) | Separate structural citation checks from semantic support; enforce honest completion and answerability states. | Test unsupported factual clauses and contradictory evidence; calibrate semantic judges against human labels and preserve uncertain results. |
