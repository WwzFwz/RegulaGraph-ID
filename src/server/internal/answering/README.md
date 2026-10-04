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

[context_builder.go](context_builder.go) kini mengepak bukti langsung dari satu snapshot dalam urutan retrieval, menghitung seluruh fragmen melalui penghitung tokenizer yang dipasok caller, serta menandai item, parent, path, dan dependency yang tidak masuk sebagai konteks parsial. [citations.go](citations.go) membangun sitasi deterministik untuk setiap klaim/bukti/sumber dari versi dan URL metadata tepercaya, menolak sumber ganda tanpa locator blob, serta membatasi klaim, jumlah sitasi, dan URL. [validation.go](validation.go) mengikat klaim/sitasi ke bukti terpilih, URL tepercaya, snapshot, versi schema, dan omission yang dihitung ulang dari bundle. Gate akhir mewajibkan setiap sumber pada bukti klaim yang didukung memiliki sitasi, memakai cache lookup URL per validasi, dan menolak prosa substantif di luar rentang claim pada status COMPLETE/PARTIAL/CONFLICT. Graph path jawaban ditolak sampai proof dirender. ID jalur graph tidak dianggap bukti jalur sudah dirender. Parent/pengecualian/path hydration, tokenizer generator aktual dan validasi semantik jawaban belum tersambung; generator draft non-streaming kini tersedia dengan prasyarat di bawah. Gate validasi tidak membuktikan hitungan token atau entailment isi klaim.

Seluruh `SourceRefs` pada satu Evidence saat ini dianggap wajib disitasi karena kontrak belum membedakan sumber bersama dari mirror alternatif; policy ini sengaja fail-closed. `SourceURLLookup` harus disuplai dari metadata yang telah dipin ke snapshot dan dibatasi deadline oleh caller; signaturenya belum membawa context atau batch prefetch. Pengukuran latency lookup/generator produksi masih diperlukan.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [citations.go](citations.go) | Helper pemetaan sumber aktif; hubungkan lookup metadata snapshot-pinned yang mempunyai deadline/batch prefetch ke generator tanpa menerima URL model. | Uji integrasi metadata nyata, URL rusak/stale, banyak sitasi, cancellation, dan p95/p99. |
| [context_builder.go](context_builder.go) | Pack ordered evidence with parent context using the exact generator tokenizer; preserve exceptions and required path sets. | Test oversized clauses, repeated parents and insufficient token budget; record omitted required evidence and actual token count. |
| [generator.go](generator.go) | Generate claims constrained to selected evidence, explicit partial/abstain/conflict states and provisional stream events. | Evaluate faithfulness and answer correctness independently; measure TTFT/completion/cost and test interrupted generation. |
| [validation.go](validation.go) | Gate struktural kini aktif; lanjutkan dukungan semantik terkalibrasi, cek token budget dari tokenizer tepercaya, dan bukti graph path yang dirender. | Tes adversarial URL/versi/context/omission/conflict/path sudah ada; gold human review dan kalibrasi semantic judge belum tersedia. |

`NewDraftGenerator` membutuhkan manifest model/prompt terpin, penghitung seluruh
prompt memakai tokenizer generator sebenarnya, output reserve, batas byte/claim,
dan policy `AllowUnreviewedDrafts=true` yang eksplisit. Permit concurrency meliputi
preflight, packing payload, token count dan provider call; pembatalan berlaku
saat antre. `Generate` hanya menghasilkan abstention aplikasi atau draft
PARTIAL dengan semua klaim UNREVIEWED. Go menghitung span UTF-8 dan membangun
sitasi dari evidence/URL tepercaya melalui `BuildDraftCitations`; model tidak
mengisi URL, offset, atau status dukungan. Mode ini belum membuktikan entailment
atau meluluskan benchmark jawaban. `BuildCitations` lama tetap khusus klaim
SUPPORTED; tidak ada pelonggaran gate jawaban COMPLETE.

[generator_test.go](generator_test.go) menguji adapter HTTP aktual dengan server
fixture, query/output-cap propagation, Unicode spans, abstention, provenance
serta output invalid. Penghitung token fixture hanya untuk kontrol alur.
Produksi harus menghitung chat template, envelope, system/question/schema dan
reserved output; provider usage yang berbeda dari hitungan terpin ditolak.
Tokenizer BGE tidak boleh dipakai untuk model jawaban Qwen.

Context kini menyertakan `status_at_knowledge_snapshot` agar UNKNOWN/CONFLICT
terlihat oleh generator. Status saat snapshot pengetahuan tidak menggantikan
filter tanggal AS_OF: versi REPEALED dapat relevan sebelum tanggal pencabutan.
Budget context menghitung label tersebut bersama seluruh teks yang dikirim.
[Integrasi terpin](../../../../doc/pinned-evidence.md) menjaga lease selama
hydration/generation; source URL prefetched tetap berasal dari bukti terverifikasi.
