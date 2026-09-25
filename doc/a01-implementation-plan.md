# Rencana implementasi A01: jawaban bersumber dan streaming terverifikasi

Dokumen ini merencanakan penyelesaian answering berdasarkan revision `17bba83`.
Perannya menentukan bagaimana EvidenceBundle dari [Q01](q01-implementation-plan.md)
menjadi konteks, klaim, citation, dan AnswerEvent melalui satu workflow Go. Nama fungsi
tambahan adalah usulan. Acuan wajib adalah [kontrak](system-contracts.md),
[verifikasi kualitas](verification-quality.md), dan [benchmark-policy.md](benchmark-policy.md).
Implementasi dilakukan sebelum full gold; hasil kualitas tetap harus dibuktikan kemudian.

## Tujuan dan fondasi aktif

Gunakan kembali `BuildContext`, `BuildCitations`, dan `ValidateGroundedAnswer`.
Ketiganya memberi fondasi packing dan validasi struktural, bukan bukti bahwa LLM sudah
menjawab dengan benar. Render proof graph, dependency context lengkap, generator,
workflow/API streaming, serta evaluasi dukungan semantik masih perlu diselesaikan.
`StructuredProvider` yang dipakai ekstraksi/resolusi dapat memberi fondasi transport;
kontrak non-streaming tersebut belum merupakan generator jawaban streaming.

Input adalah pertanyaan, scope/snapshot/manifest terpin, dan bundle bukti terverifikasi.
Output adalah Answer dengan claim spans UTF-8, citation ke sumber/versi, status semantik,
completeness, serta event stream yang memiliki tepat satu terminal FINAL atau ERROR.
COMPLETE, PARTIAL, ABSTAIN, CONFLICT, dan NEEDS_CLARIFICATION dibedakan dari sukses/gagal
transport. Jawaban abstain yang valid bukan otomatis kegagalan HTTP atau kualitas.

## 1. Audit kontrak sebelum generator

- Formalisasikan required evidence groups dan rendered graph proof bersama Q01.
  Path ID saja tidak membuktikan bahwa semua edge/support/kondisi sudah masuk konteks.
  Tambahkan kontrak bertipe bila ContextBundle belum dapat membawa proof tersebut;
  sinkronkan validator/binding lintas runtime tanpa mengubah baseline diam-diam.
- Audit constraint `Answer.effective_dates` dan `StreamMeta.effective_dates` ketika klarifikasi tanggal diperlukan.
  Jangan mengarang tanggal agar jawaban lolos validator. Default waktu server hanya
  boleh digunakan untuk policy default yang eksplisit, bukan mengganti tanggal yang
  sedang ditanyakan pengguna. Perubahan constraint memerlukan review kompatibilitas.
- Perjelas citation untuk joint support vs alternative/mirror support. Implementasi
  sekarang meminta citation semua SourceRefs pada evidence; jangan diam-diam membuang
  salah satunya tanpa kontrak yang membedakan semantik dukungan tersebut.
- Pisahkan structural validation dari semantic support assessment. `SUPPORTED` tidak
  boleh diberikan hanya karena ID citation valid atau model menyatakan dirinya benar.
  Audit kebutuhan metadata metode/versi/evidence penilaian; kasus yang belum dinilai
  tetap UNREVIEWED, dan klaim tidak boleh disajikan sebagai terverifikasi.
- Seluruh teks faktual harus tercakup claim/segment tervalidasi, bukan hanya entries
  `Claim` yang kebetulan diberikan model. Generator menghasilkan segmen klaim bertipe;
  Go merender penghubung/boilerplate terbatas yang tidak menambah fakta. Provider tidak
  dapat melabeli prosa faktual sebagai boilerplate untuk melewati pemeriksaan. Tolak
  teks provider di luar segmen yang diterima, gap coverage, span bertabrakan yang
  ambigu, serta final text yang tidak sama dengan hasil assembly yang divalidasi.
- Tetapkan semantik event provisional dan final. Teks yang sudah tampil sebelum
  validasi final bukan jawaban final terverifikasi. Mode buffer sampai validasi dan
  mode provisional mempunyai biaya latency berbeda dan harus dicatat dalam run profile.

## 2. Komponen dan fungsi yang direncanakan

Path relatif terhadap `src/server/internal/`; file aktif diperluas tanpa menghilangkan
deskripsi fungsi, kontrak, benchmark, atau perhatian integrasinya.

| Lokasi | Fungsi usulan/pengembangan | Peran dan kontrak |
| --- | --- | --- |
| `answering/context_dependencies.go` | `ResolveContextDependencies`, `RenderGraphProof` | Hidrasi parent/definisi/syarat/pengecualian dan path supports pada snapshot yang sama; proof terikat rendered blocks. |
| `answering/context_builder.go` | perluas `BuildContext`, `ValidateContextBudget` | Packing kelompok bukti koheren dengan token count generator sebenarnya dan omission eksplisit. |
| `adapters/inference/answer_tokens.go` | `CountAnswerPromptTokens` | Menghitung system/chat template, pertanyaan, bukti, schema/tool wrapper dan output reserve memakai tokenizer/policy model generator terpin. |
| `answering/generator.go` | `GenerateGroundedAnswer`, `AssembleClaims` | Konteks + prompt terpin → draft klaim terstruktur; ID bukti dibatasi ke context, offsets dihitung dari final text. |
| `adapters/inference/answer_provider.go` | `GenerateAnswer`, `StreamAnswerSegments` | Transport model lokal/provider yang dipilih; capability negotiation, deadline, ukuran frame/output dan usage. |
| `answering/validation.go` | `ValidateDraftSupport`, perluas `ValidateGroundedAnswer` | Validasi ID, qualifiers, versi, span, completeness; penilaian semantik terpisah dengan metode dan uncertainty yang tercatat. |
| `answering/citations.go` | perluas `BuildCitations` | Mapping claim-evidence-source deterministik; URL/locator dari registry terverifikasi, bukan keluaran model. |
| `workflows/answer.go` | `AnswerQuestion`, `StreamQuestion` | Memiliki read lease dari admission sampai hidrasi/validasi final/terminal; meneruskan pin yang sama ke Q01 dan seluruh lookup. |
| `api/` handler dan schema yang relevan | endpoint question/event stream | Validasi, autentikasi/otorisasi, admission, backpressure dan serialisasi; seluruh logika jawaban melalui workflow. |
| `cmd/cli/` serta adapter evaluator | command question/export run | Menggunakan endpoint/workflow/artefak produksi yang sama; bukan salinan retrieval atau generator. |

## 3. Context dan penilaian klaim

Context memuat bukti langsung, konteks induk yang diperlukan, syarat/pengecualian, serta
graph proof yang memang mendukung jawaban. Pilih kelompok dependency yang koheren;
jika tidak muat, keluarkan omission dan PARTIAL/ABSTAIN yang tepat. Jangan memangkas
pengecualian demi token budget lalu menyatakan klaim utuh. Konflik sumber/versi tidak
diselesaikan dengan mengambil teks yang ranking-nya paling tinggi saja.

Token budget harus menggunakan tokenizer dan chat template generator; tokenizer BGE
bukan penggantinya. Sediakan tokenizer lokal yang cocok atau capability count yang
dapat dibuktikan dari provider. Jika tidak tersedia, jangan mengklaim exact budget
atau readiness profil tersebut. Ukur overhead packing; penghitungan ulang seluruh
prefix pada setiap insertion dapat mahal. Optimasi melalui reuse terverifikasi dan
penghitungan final menyeluruh, bukan menjumlahkan token fragmen dengan asumsi selalu aditif.

Generator mengeluarkan klaim beserta evidence IDs dan qualifiers. Teks dokumen diperlakukan
sebagai data, bukan instruksi untuk model atau akses tool. Output dibatasi schema,
ukuran, jumlah klaim dan ID yang diizinkan. Provider tidak menentukan URL sumber atau
membuat canonical baru. Span klaim dihitung saat Go menyusun teks final dengan offset
byte UTF-8 start-inclusive/end-exclusive, bukan mempercayai angka offset dari LLM.

Validasi struktural diikuti pemeriksaan angka/tanggal/satuan/negasi, dukungan dependency,
dan penilaian semantik yang metodenya terpin. Aturan deterministik dapat menemukan
kontradiksi tertentu; aturan itu tidak membuktikan semua interpretasi hukum. Bila
memakai model judge, pisahkan hasilnya dari self-report generator, catat uncertainty,
dan kalibrasikan terhadap review manusia setelah G01. Kebijakan apakah UNREVIEWED
boleh tampil sebagai jawaban sementara harus eksplisit; profil yang mensyaratkan
verified support tidak boleh mengubahnya menjadi SUPPORTED untuk meluluskan output.

URL/locator/source metadata dihidrasi batch sebelum membangun citation, dengan
deadline dan pin scope; jangan membuka lookup jaringan per klaim. Validasi seluruh
claim-evidence-source mapping kembali sebelum terminal FINAL.

## 4. Streaming, provider, dan isolasi resource

Dukung generator non-streaming terlebih dahulu untuk correctness, kemudian streaming
melalui kontrak provider yang menyatakan capability. Provider non-streaming tidak
diberi label token-streaming hanya karena hasil akhirnya dikirim dalam potongan.
Untuk structured streaming, parse frame secara incremental dan emit hanya segmen
teks yang sudah lengkap/valid menurut grammar; raw token JSON bukan teks jawaban.
Uji frame terbelah, UTF-8 parsial, escape, urutan klaim dan batas memory parser.

META mendahului delta; sequence monoton per request. Mode provisional menyatakan teks
belum final. FINAL harus konsisten dengan teks yang sudah dikirim, atau protokol harus
memiliki mekanisme replacement eksplisit yang disepakati sebelum implementasi. Jika
validasi akhir gagal setelah delta, emit ERROR/invalidated status sesuai kontrak dan
jangan menerbitkan FINAL sukses. Mode strict menahan teks sampai validasi selesai;
ukur first visible token dan first verified answer secara terpisah. Jangan menyamarkan
buffering atau waktu validasi sebagai latency provider saja.

Gunakan koneksi/model reusable, admission bounded, deadline yang menyebar sampai
provider, dan backpressure untuk client lambat. Disconnect membatalkan pekerjaan
yang tak lagi diperlukan. Retry sebelum output hanya jika policy/biaya/idempotency
memungkinkan; jangan otomatis regenerasi setelah token terlihat. Batasi queue query
terpisah dari bulk ingestion. Rahasia provider berasal dari environment/secret config,
tidak disimpan pada prompt, fixture, log, atau commit. Endpoint/model lokal diprioritaskan
sesuai arahan pengguna; provider/model akhir dan aksesnya dipin sebelum pengujian nyata.

Workflow jawaban memiliki read lease sampai tidak ada lagi pembacaan/validasi bukti
dan terminal dikirim atau stream dibatalkan. Q01 menerima pin ini tanpa melepasnya.
Renew lease sebelum kedaluwarsa; kegagalan renewal membatalkan proses secara eksplisit.
Requested historical snapshot melewati admission published/retained/authorized yang
sama dengan Q01. Cleanup pada semua jalur error/disconnect wajib; deadline dan batas
client lambat mencegah lease menahan garbage collection tanpa batas.

## 5. Validasi yang diperlukan

| Area | Kasus dan expected behavior |
| --- | --- |
| Context | Parent/exception hilang, dua sumber konflik, path tidak lengkap dan kelompok bukti melebihi budget: omission/partial eksplisit, tidak ada klaim completeness palsu. |
| Token | Bahasa Indonesia/code-switch, Unicode, chat template/schema overhead dan reserve output: budget sesuai generator sebenarnya; mismatch tokenizer/config ditolak. |
| Claim/citation | ID/URL karangan, angka berubah, negasi hilang, source/version salah, locator ambigu, span UTF-8: invalid claim tidak lolos sebagai verified answer. |
| Coverage teks | Satu klaim sah diikuti kalimat faktual tanpa dukungan di luar claim span, atau model menyebutnya boilerplate: output ditolak, bukan lolos karena daftar Claim yang diberikan valid. |
| Prompt/data boundary | PDF atau pertanyaan berisi instruksi mengabaikan sumber/membuka secret: data tidak menjadi otorisasi/tool call, jawaban tetap terikat evidence allowlist. |
| Abstain/conflict | Tanpa bukti, unknown date, pertanyaan ambigu dan bukti bertentangan: status sesuai situasi, tidak mengarang tanggal atau jawaban hanya agar nonempty. |
| Streaming | Frame rusak/terbelah, disconnect, lambat, cancel, provider gagal dan validasi gagal setelah delta: memory bounded, sequence benar, tepat satu terminal, tidak ada FINAL sukses palsu. |
| Konsistensi | Update corpus ketika generator berjalan, source lookup lintas corpus dan cache beda izin: seluruh hasil tetap pada pin/otorisasi awal. |
| Read lease | Q01 selesai saat generator masih berjalan, GC snapshot historis, renewal gagal, disconnect dan terminal error: pin tetap dimiliki A01 sampai aman dilepas; tidak ada pembacaan artefak tanpa lease. |
| Integrasi nyata | PDF terverifikasi → indeks/graph → Q01 → model lokal → jawaban/citation/API/CLI → bundle E01; raw timing dan token usage dapat diaudit. |

## 6. Paket pengerjaan dan penerimaan

Urutan commit: (1) kontrak context/support/event, (2) dependency hydration dan exact
token budget, (3) generator/provider non-streaming dan validation, (4) workflow/API/CLI,
(5) streaming/cancellation/backpressure, (6) evaluasi artefak dan dokumentasi milestone.
Agent independen memeriksa boundary retrieval-answer, prompt/data/auth, event terminal,
dan hasil integrasi berdasarkan bukti, bukan klaim implementer.

Catat context-build latency, queue, TTFT, first verified answer, total p50/p95/p99,
tokens/biaya, memory, citation coverage dan error/abstention. Angka required tetap
[benchmark-targets.yaml](../configs/benchmark-targets.yaml); workload, denominator dan
profil tidak boleh diubah untuk melewati gate. Optimasi kecepatan tidak membenarkan
hilangnya bukti wajib atau status provisional yang disembunyikan.

Setelah kode X01/K01/Q01/A01 dan incremental U01 tersambung, lanjutkan G01 human gold,
kalibrasi judge/tuning pada dev, lalu acceptance test B01. Tanpa gold sah, kualitas
jawaban/citation/faithfulness tetap NOT_MEASURED; tanpa prasyarat runtime/workload,
gate terkait BLOCKED. Dokumen ini tidak mencakup deployment dan tidak menyatakan
release selesai hanya karena build, fixture, atau smoke end-to-end lulus.
