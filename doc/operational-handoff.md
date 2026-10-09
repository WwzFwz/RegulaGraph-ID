# Panduan melanjutkan sampai aplikasi bisa digunakan

Dokumen ini adalah catatan serah-terima pekerjaan agar pengembangan dapat
dilanjutkan setelah pergantian sesi/model atau token habis. Isinya adalah kondisi
aktual, urutan pekerjaan, lokasi kode, cara verifikasi, dan titik berhenti yang
aman. Ini bukan pernyataan bahwa pipeline lengkap sudah berjalan. Pembagian
pekerjaan mengikuti dependency, tidak dibatasi nomor tahap pada percakapan.

**Checkpoint: 2026-10-09; baseline answering `fb6c02c`.** Periksa `git status` dan
`git log` sebelum melanjutkan karena commit setelah baseline dapat menutup bagian
yang masih terbuka di sini. Status terbaru berada di [development-plan](development-plan.md)
dan laporan verifikasi bertanggal; beberapa dokumen desain lama masih mempunyai
paragraf status historis. Cocokkan status dengan implementasi dan bukti run.

## Tujuan operasional dan batas klaim

Tujuan yang sedang dikejar: PDF resmi lokal dapat diproses menjadi chunk, graph
dan indeks, diterbitkan sebagai snapshot konsisten, lalu dipakai untuk mencari
bukti dan menjawab pertanyaan melalui CLI/API dengan citation. Operator mengetahui
cara menyalakan, mematikan, memeriksa kegagalan dan melanjutkan proses.

Gold dataset dan acceptance kualitas/performa dapat dikerjakan setelah jalur
operasional tersedia. Keduanya **tetap kewajiban proyek keseluruhan**; target dalam
`configs/benchmark-targets.yaml` tidak berubah. Validasi input, kutipan sumber,
identitas/versi, snapshot, keamanan dasar, dan integritas storage tetap diperlukan
agar versi operasional tidak menerbitkan data rusak. Deployment dikecualikan dari
pekerjaan yang diotorisasi saat ini; menjalankan layanan lokal bukan deployment.

Demo BM25 lokal sudah tersedia melalui `scripts/start_demo.ps1`. Demo itu berguna
untuk mencoba UI tetapi tidak membuktikan jalur Hybrid GraphRAG produksi selesai.
GUI lengkap untuk pipeline produksi belum menjadi hasil yang dibuktikan di sini;
CLI/API adalah antarmuka operasional yang sudah mempunyai implementasi.

Prioritas panduan ini adalah **aplikasi dapat digunakan dari awal sampai akhir**.
Penomoran A–D di bawah adalah pembagian pekerjaan dokumen ini, bukan pemetaan
nomor 1–4 percakapan. Pengujian gold dan benchmark release dipisahkan dari target
operasional; akses query dan generation jawaban tetap diperlukan untuk aplikasi
tanya-jawab. Smoke fungsional tetap dilakukan, termasuk penolakan input/bukti
invalid, agar kegagalan tidak disajikan sebagai jawaban sukses.

| Urutan praktis | Hasil yang harus tersedia untuk pemakai |
| --- | --- |
| Siapkan corpus | PDF dan metadata terdaftar, chunk dapat ditelusuri ke sumber; sudah ada contoh nyata |
| Siapkan pencarian | Embedding dan BM25 terbit pada snapshot konsisten; query dapat mengambil teks bukti |
| Lengkapi graph | EXTRACT, RESOLVE dan ASSEMBLE menghasilkan relasi berbukti, lalu graph diterbitkan |
| Hubungkan jawaban | Query mengambil bukti, generator membuat jawaban, citation divalidasi, CLI/API dapat dipakai |
| Pastikan dapat diulang | Konfigurasi, perintah start/stop/resume, serta penanganan gagal tercatat |

Hybrid retrieval dapat diuji setelah indeks terbit sambil cabang graph diperbaiki.
Itu kemajuan operasional, tetapi belum berarti seluruh Hybrid GraphRAG siap.

## Ringkasan keadaan sekarang

| Bagian | Bukti yang sudah ada | Yang belum selesai |
| --- | --- | --- |
| Akuisisi PDF | Collector, audit, impor immutable dan submit job tersedia | Coverage sumber lengkap, semua jenis PDF/scan belum teruji |
| PARSE → STRUCTURE → BIND → CHUNK | Satu PDF BPK nyata berhasil menjadi 35 chunk dengan PDFium/tokenizer aktual | Perlu menjalankan inventory operasional yang dipilih; scan/OCR/layout sulit belum lengkap |
| EXTRACT | Prompt/schema, ontology, exact-source projection, cap dan admission lokal tersedia | Model pada chunk pembuka masih menghasilkan locator salah atau completion gagal; belum satu full-PDF EXTRACT berhasil |
| Recovery EXTRACT | Completion tersimpan di PostgreSQL dan divalidasi ulang setelah restart; tes database nyata lulus | Full-PDF restart dengan model nyata belum dibuktikan; bukan exactly-once sampling |
| RESOLVE | Kandidat, konteks bukti, proposal LINK/DEFER, review dan registry commit tersedia | Entitas tanpa kandidat membutuhkan jalur pembuatan canonical yang sah; CREATE/MERGE/SPLIT semantik lengkap belum tersedia |
| Native embedding/reranking | BGE-M3 C++/ONNX sudah mengindeks 35 chunk PDF nyata; reranking mempunyai bukti fixture | Reranking pada corpus ini dan acceptance model belum dibuktikan |
| Index + graph publication | Indeks dense/BM25 PP 12/2006 published dan dapat dibaca; graph teruji pada fixture | Graph dari ekstraksi/resolusi PDF nyata belum published |
| Retrieval + draft answer | Hybrid evidence, CLI `-answer`, dan API `/v1/questions` pada snapshot PDF nyata berhasil; satu claim/citation, PARTIAL dan UNREVIEWED eksplisit | Graph nyata, cakupan pertanyaan dan kecepatan interaktif belum terbukti |

### Checkpoint inventory persisten PP 12/2006

Percobaan lokal berikut berbeda dari fixture tes yang membuat corpus baru tiap
invocation. Artefak di bawah diabaikan Git; **tidak otomatis tersedia pada clone
baru**. Jangan membagikan credential DB dalam dokumen atau commit.

| Identitas | Nilai |
| --- | --- |
| Root run | `artifacts/operational/pp12-v1/` |
| Schema PostgreSQL lokal | `regulagraph_ops_pp12_v1` |
| Corpus / scope | `corpus:pp12-operational-v1` / `operator:pp12-operational-v1` |
| Source job | `job:pp12-operational-v1` |
| Publication / generation aktif | `publication:pp12-index-v2` / `generation:pp12-index-v2` |
| Collection aktif | `regulagraph_pp12_operational_v2` |
| INDEX child berhasil | `index-job-v1:811e0c734caee2f7a74795a294ffb462ea12f1df80695f39ef453673c75a7460` |

Yang telah berhasil: CLI submit, PARSE → STRUCTURE → BIND → CHUNK menghasilkan
35 chunk; preparation snapshot, vocabulary, dictionary, statistics dan scheduling
INDEX berhasil. Angka 35 adalah chunk, bukan jumlah PDF. EXTRACT dimatikan untuk
run ini. Profil/request sumber belum memuat model semantic; jangan sekadar
menyalakan EXTRACT untuk menganggap request tersebut sudah siap menjalankan model.
Siapkan request/producer semantic terpin melalui admission yang berlaku.

**Riwayat kegagalan v1, sudah dipulihkan melalui publication v2:** PostgreSQL menunjukkan
child `JOB_STATE_FAILED` (9), stage INDEX (7), attempt 8. Worker gagal startup
karena konfigurasi native memakai `host:port`, padahal Rust membutuhkan URL HTTP.
Coordinator terlanjur berjalan tanpa worker dan menghabiskan delapan attempt
dengan connection refused. Ini kegagalan konfigurasi/urutan startup pada percobaan,
bukan bukti model embedding gagal. Source CHUNK tetap STAGED (4), stage CHUNK (9).

`environment.ps1` lokal telah dikoreksi menjadi:

```powershell
$env:REGULAGRAPH_WORKER_NATIVE_ENDPOINT = 'http://127.0.0.1:55072'
$env:REGULAGRAPH_QUERY_NATIVE_ENDPOINT = '127.0.0.1:55072'
```

Koreksi sudah diuji: native dan worker berhasil startup, lalu INDEX v2 berhasil
pada attempt pertama. `abort-index` menutup publication v1 tanpa mengubah job gagal.
Snapshot/statistics dibuat ulang untuk v2 melalui CLI dan Rust population;
`publish-index` berhasil setelah pemeriksaan backend. Hybrid query berhasil setelah
perbaikan hidrasi nested text. Hasil contoh 29 evidence items berstatus PARTIAL,
dengan 38 dependency konteks/temporal yang belum lengkap; bukan acceptance kualitas.

Coordinator dan worker v2 sudah dihentikan setelah selesai. Native inference
dipertahankan di `127.0.0.1:55072` untuk query (catatan proses
`native-resume-process.json`). Periksa proses/listener aktual saat resume. Container
backend dan Ollama lain tidak dihentikan. File process/PID lama hanya riwayat dan
tidak boleh dianggap status hidup. Read lease tersisa nol pada pemeriksaan akhir.

Bukti lokal: `logs/worker-index.stderr.log`, `logs/coordinator-index.stdout.log`,
`handoff-job-state.txt`, konfigurasi `environment.ps1`, serta artefak `preparation/`
di root run. Simpan kegagalan ini saat membuat laporan keberhasilan berikutnya.

Langkah pemulihan yang sudah dilakukan, untuk acuan kegagalan serupa:

1. Periksa kembali DB/job dan artefak sebelum menyalakan coordinator. Jangan
   menghapus row gagal atau mereset counter secara manual.
2. Jalankan native dan worker dengan konfigurasi yang diperbaiki. Pastikan proses
   tetap hidup, listener siap, dan pin manifest sesuai sebelum coordinator mulai
   mengambil job. Pada PowerShell gunakan `$ErrorActionPreference = 'Stop'` dan
   periksa `$LASTEXITCODE` setelah perintah native.
3. Jalankan `abort-index` untuk inventory admitted yang gagal sebelum published.
   Retry/restart tidak mengantrekan ulang child terminal. Buat publication baru
   melalui planner/admission dengan identitas/preparation baru serta bekukan ulang
   dependency snapshot/statistics; jangan menyalin receipt lama. Jangan melakukan
   abort pada v2 yang kini sudah published.
4. Sesudah seluruh child INDEX benar-benar STAGED, jalankan `publish-index`,
   kemudian `query-evidence -profile hybrid`. Simpan readback, snapshot dan teks
   bukti sebelum menyatakan pencarian operasional berhasil.

**Resume sekarang:** gunakan snapshot v2 yang sudah published, tidak perlu
membangun ulang corpus untuk mencoba query. Dari root repo, set DSN schema
operasional secara lokal lalu dot-source `artifacts/operational/pp12-v1/environment.ps1`.
Pastikan PostgreSQL/Qdrant dan native siap; jalankan:

```powershell
./artifacts/operational/pp12-v1/bin/cli.exe query-evidence -question 'Kapan PP Nomor 12 Tahun 2006 mulai berlaku?' -as-of 2026-01-01 -profile hybrid -unresolved report -limit 20 -timeout 5m
```

Raw evidence berada pada `logs/query-hybrid-v2.json`, `logs/query-effective-v2.json`
dan `logs/publish-index-v2.json`; metadata v2 pada `preparation-v2/`. PowerShell
redirect pada run ini menghasilkan UTF-16, bukan ProtoJSON UTF-8 wire files.
Lihat [laporan pemulihan dan query nyata](verification-report-real-index.md).
CLI `-answer` kini berhasil pada pertanyaan tanggal berlaku tersebut. Lihat
[laporan generation nyata](verification-report-real-answer.md) untuk hasil,
riwayat timeout, pin konfigurasi dan batas pembuktian. Pekerjaan A/B untuk graph
nyata tetap diperlukan; keberhasilan GENERATE tidak berarti EXTRACT sudah valid.

### Memakai snapshot yang sudah tersedia

Tidak perlu download atau membangun ulang PP 12/2006 untuk mencoba jalur ini.
Clone baru tetap memerlukan model, artifact store dan database; direktori
`artifacts/operational/pp12-v1/` hanya checkpoint lokal yang diabaikan Git.
Periksa layanan yang sudah hidup sebelum menjalankan instance lain.

| Layanan | Endpoint checkpoint | Dibutuhkan untuk |
| --- | --- | --- |
| PostgreSQL | `127.0.0.1:55448`, schema `regulagraph_ops_pp12_v1` | Registry, snapshot dan read lease |
| Qdrant | `http://127.0.0.1:56348` | Dense/BM25 dari collection v2 |
| Native BGE-M3 | `127.0.0.1:55072` | Embedding pertanyaan |
| llama.cpp Qwen | `http://127.0.0.1:55110`, context 8192 | GENERATE dengan tokenizer aktual |
| Go API | `http://127.0.0.1:58097` | HTTP evidence/draft, memakai bearer token |

Untuk query snapshot ini, ingestion coordinator/worker dan semantic gateway
tidak perlu dijalankan. Neo4j baru diperlukan saat memakai profil graph yang
sudah dipublikasikan. Runtime BGE dan model GENERATE berbeda dan keduanya
dibutuhkan untuk hybrid answer.

Konfigurasi GENERATE yang berhasil berada di `answer/config-compact.json` di
root run, dengan SHA-256
`eb1a0cac9365aadc8fed4aa5b37b361c38ff7e8a0dc7aa44349526fd21760099`.
Key server model checkpoint disimpan lokal pada `answer/model-key.local`;
token client API pada `answer/api-token.local`. Keduanya diabaikan Git. Untuk
runtime baru buat secret sendiri dan pasang nilai yang sama di server/client
pasangannya; tidak memerlukan API key cloud untuk profil lokal ini.
Gunakan binary yang memuat `fb6c02c` atau implementasi kompatibel; `cli.exe`
lama tidak memuat prompt baru. `config.json` (4096) dan `config-8192.json`
(prompt lama) disimpan sebagai riwayat, bukan konfigurasi terbaru.

Di PowerShell dari root repo, setelah backend/native/model siap:

```powershell
# Isi DSN lokal beserta search_path schema di atas; jangan commit credential.
$env:REGULAGRAPH_POSTGRES_DSN = '<DSN operasional dari konfigurasi lokal>'
. ./artifacts/operational/pp12-v1/environment.ps1
$env:REGULAGRAPH_BUILD_ID = (git rev-parse --short HEAD).Trim()
$env:REGULAGRAPH_ANSWER_CONFIG = (Resolve-Path './artifacts/operational/pp12-v1/answer/config-compact.json').Path
$env:REGULAGRAPH_ANSWER_CONFIG_SHA256 = (Get-FileHash $env:REGULAGRAPH_ANSWER_CONFIG).Hash.ToLowerInvariant()
$env:REGULAGRAPH_ANSWER_API_KEY = (Get-Content './artifacts/operational/pp12-v1/answer/model-key.local' -Raw).Trim()
go run ./src/server/cmd/cli query-evidence -answer -question 'Kapan PP Nomor 12 Tahun 2006 mulai berlaku?' -as-of 2026-01-01 -profile hybrid -unresolved report -limit 20 -timeout 5m
```

Output yang dibuktikan berupa `mode: answer_draft`, satu claim/citation,
`COMPLETION_STATUS_SUCCEEDED`, `SEMANTIC_STATUS_PARTIAL`, dan
`SUPPORT_STATUS_UNREVIEWED`; daftar `missingEvidence` tetap lengkap.
Satu run CLI memerlukan sekitar 182 detik di CPU; API 200 sekitar 155 detik,
bukan hasil benchmark release atau perbandingan cold/warm yang terkontrol.
Untuk evidence saja, hilangkan `-answer`; model GENERATE tidak diperlukan.

Jika llama.cpp belum hidup, jalankan di terminal tersendiri. Isi key lokal
lebih dahulu, sama dengan terminal CLI/API. Binary dan GGUF di bawah adalah
artefak checkpoint, bukan bagian yang otomatis tersedia setelah clone:

```powershell
$answerConfig = Get-Content './artifacts/operational/pp12-v1/answer/config-compact.json' -Raw | ConvertFrom-Json
$env:REGULAGRAPH_ANSWER_API_KEY = (Get-Content './artifacts/operational/pp12-v1/answer/model-key.local' -Raw).Trim()
./.cache/llama-b11515/bin/llama-server.exe --model $answerConfig.gguf_path --alias regulagraph-local-qwen --host 127.0.0.1 --port 55110 --ctx-size 8192 --parallel 1 --threads 4 --threads-batch 4 --n-gpu-layers 0 --no-context-shift --api-key $env:REGULAGRAPH_ANSWER_API_KEY
```

Cara menyiapkan BGE/CUDA tersedia pada [native inference](native-inference.md).
Gunakan bundle `artifacts/models/bge-m3-fp16-ort1220`, listener `127.0.0.1:55072`,
dan manifest pin
`ac099586f048edb5caa1e13fa10ccbaaad5aef3e13f07c1b9bc88e1f279ca35d`.
Jangan mengganti model/query manifest tanpa memeriksa kesesuaian indeks published.

Untuk API, di terminal dengan environment query/answer di atas, gunakan:

```powershell
$env:REGULAGRAPH_API_LISTEN = '127.0.0.1:58097'
$env:REGULAGRAPH_API_AUTH_SCOPE = 'operator:pp12-operational-v1'
$env:REGULAGRAPH_API_PROFILE = 'hybrid'
$env:REGULAGRAPH_API_ANSWERS = 'true'
$env:REGULAGRAPH_API_TIMEOUT = '5m'
$env:REGULAGRAPH_API_TOKEN = (Get-Content './artifacts/operational/pp12-v1/answer/api-token.local' -Raw).Trim()
go run ./src/server/cmd/api
```

Di terminal client, isi token yang sama, lalu kirim request C01. JSON HTTP memakai
snake_case; query date tidak menyatakan bahwa status keberlakuan sudah tervalidasi.

```powershell
$headers = @{ Authorization = 'Bearer ' + (Get-Content './artifacts/operational/pp12-v1/answer/api-token.local' -Raw).Trim() }
Invoke-RestMethod 'http://127.0.0.1:58097/readyz' -Headers $headers
$request = @{
  corpus_id = 'corpus:pp12-operational-v1'
  question = 'Kapan PP Nomor 12 Tahun 2006 mulai berlaku?'
  response_mode = 'RESPONSE_MODE_COMPLETE'
  requested_profile = 'RETRIEVAL_PROFILE_HYBRID_RAG'
  temporal_scope = @{
    mode = 'TEMPORAL_MODE_AS_OF'
    effective_at = @{ year = 2026; month = 1; day = 1 }
    unresolved_policy = 'UNRESOLVED_POLICY_REPORT'
  }
} | ConvertTo-Json -Depth 5
Invoke-RestMethod 'http://127.0.0.1:58097/v1/questions' -Method Post -Headers $headers -ContentType 'application/json' -Body $request -TimeoutSec 315
```

Checkpoint API compact berhasil HTTP 200 dengan input di atas; catatan proses
ada pada `answer/api-compact-process.json` (PID saat run 33976, jangan diasumsikan
tetap hidup). Model tercatat pada `answer/process-8192.json` (PID saat run 39844).
`answer/api-token.local` memuat token run yang diabaikan Git; jangan membagikannya.

`/v1/evidence` dengan request sama hanya mengambil bukti. `readyz` yang berhasil
tidak sendirian membuktikan generation atau kualitas jawaban. Ctrl+C menghentikan
proses foreground; untuk proses background gunakan pengecekan PID/path di bagian
operasi bawah. Jangan `docker compose down -v` atau menghapus schema saat berhenti;
data/snapshot dibutuhkan untuk resume. Menyalakan kembali query tidak memerlukan
ulang download, embedding atau extraction apabila snapshot masih utuh.

## Urutan dependency yang benar

```text
PDF + metadata → import/submit → PARSE → STRUCTURE → BIND → CHUNK
                                                            |
                         +----------------------------------+-------------------+
                         |                                                      |
                         v                                                      v
                EXTRACT → RESOLVE/review                         snapshot → vocabulary
                         |                                      → dictionary/statistics
                         |                                      → INDEX → publish-index
                         |                                                      |
                         +---------------------+--------------------------------+
                                               v
                             prepare-graph → ASSEMBLE → publish-graph
                                               |
                                               v
                         query-evidence → optional rerank → answer + citations
```

**Publication indeks mendahului preparation graph pada implementasi sekarang.**
Cabang indeks dapat dikerjakan dari CHUNK sambil memperbaiki EXTRACT/RESOLVE.
Graph membutuhkan seluruh sumber indeks mempunyai resolusi dan authority valid;
DEFER tidak otomatis membuat graph siap diterbitkan.

Untuk menyiapkan dokumen/indeks tanpa menjalankan model EXTRACT, set
`REGULAGRAPH_EXTRACT_ENABLED=false` sebelum menyalakan coordinator. Default `true`.
Flag menunda claim EXTRACT pada proses itu saja; tidak mengubah checkpoint atau
mengklaim ekstraksi selesai. Semua coordinator yang dapat mengambil sumber yang
sama harus memakai pilihan konsisten. Restart dengan `true` setelah model siap.
Lihat [kontrak coordinator](../src/server/cmd/ingestion-worker/README.md).

## Pekerjaan A — selesaikan ekstraksi sumber nyata

**Checkpoint lanjutan:** tersedia v3 opsional dengan rentang sumber bernomor;
default v1/v2 tidak berubah. Tes boundary/replay lulus, tetapi eksperimen Qwen CPU
pada full chunk yang sama timeout 600 detik, belum ada completion valid.
[Laporan](verification-report-indexed-extraction.md). Pengguna menyatakan model
lebih kuat dapat dipilih nanti; gunakan [panduan pergantian model](semantic-model-profiles.md)
dan jangan mengulang sampling Qwen tanpa perubahan yang beralasan. Ini tidak
menghilangkan kewajiban menghubungkan EXTRACT ke RESOLVE/graph saat model siap.

**Input:** DocumentBatch CHUNK, teks normalisasi, provenance, ontology dan manifest
model/prompt/schema yang benar-benar digunakan. **Output:** ExtractionBatch valid
untuk seluruh item, terdaftar dan di-checkpoint coordinator.

Langkah berikutnya:

1. Gunakan chunk gagal yang sama untuk membandingkan konfigurasi. Catat semua
   request, response, hash, waktu, usage dan finish reason; jangan membuang attempt
   gagal. Perbaikan tidak boleh berupa menghapus fakta sulit atau melonggarkan
   exact-source validation.
2. Setelah satu item valid, cek isi faktanya: output kosong atau tanpa relasi
   bukan keberhasilan hanya karena JSON dapat di-parse. Smoke ini belum gold eval.
3. Bekukan profil yang dipilih, ekspor producer aktual, samakan pin Go/Rust, lalu
   jalankan seluruh PDF. Perubahan prompt/schema/config perlu identitas producer
   baru; jangan mengubah hasil historis.
4. Aktifkan completion replay EXTRACT dan buktikan restart pada input/corpus/batch
   yang sama. Completion yang sudah tersimpan tidak perlu disampling ulang, tetapi
   tetap wajib melewati semantic validation. Model invalid yang tersimpan akan
   tetap invalid saat replay; retry identik bukan perbaikan kualitas.

Kode utama: `src/server/internal/adapters/inference/semantic.go`,
`semantic_quote_spans.go`, `semantic_replay.go`, `llm_request.go`,
`src/ingestion/src/worker/processor.rs`, dan
`src/server/internal/workflows/acquisition_pipeline_integration_test.go`.
Konfigurasi: `.env.example`, `configs/prompts/extraction-v2.md`,
`src/contracts/jsonschema/extraction-output-v2.json`.

**Syarat selesai A:** seluruh chunk yang dipilih terhitung; tidak ada completion
terpotong/locator invalid yang diterima; ExtractionBatch dan checkpoint valid;
restart tidak menyamarkan model failure atau mengulang item committed secara
tidak perlu. Simpan bukti aktual, bukan hanya tes provider mock.

### Titik reproduksi yang sudah tersedia

PDF PP 12/2006 mempunyai SHA-256
`c2c761194c0a15d1858c38c7c45308e045539a80be5fd67b23cd4a050f7dbbfb`.
Record acquisition:

```text
data/acquisition/records/423b6a542356f377cf9c0bfe1f3382c91700e2464ab82a06946add0204bf91c9.json
```

[Bukti PARSE sampai CHUNK](verification-report-real-pdf.md) mencatat artefak dan
perintah. Tes `TestAcquiredPDFThroughNativeDocumentPipeline` memakai database
terisolasi dan corpus baru per invocation; itu bukan inventory operasional
persisten. Corpus baru menghasilkan replay key baru. Jangan mengklaim dua run
tes terpisah membuktikan reuse completion untuk job/corpus yang sama.

```powershell
# Hanya setelah worker, DB test terisolasi, artefak dan environment test siap.
# REGULAGRAPH_TEST_DOCUMENT_WORKER
# REGULAGRAPH_TEST_ACQUISITION_ROOT
# REGULAGRAPH_TEST_ACQUISITION_RECORD
# REGULAGRAPH_TEST_DOCUMENT_ARTIFACT_ROOT
# REGULAGRAPH_TEST_EXTRACTION_PRODUCER: file producer EXTRACT untuk mengaktifkan tahap ini
go test ./src/server/internal/workflows -run '^TestAcquiredPDFThroughNativeDocumentPipeline$' -count=1 -v
```

Tes saat checkpoint ini memiliki context keseluruhan 10 menit dan RPC PARSE
executor 4 menit. Model yang lambat pada 35 chunk dapat melampauinya. Profil
diagnostik baru harus menyatakan deadline/lease dan alasan perubahan; jangan
melaporkan deadline yang diperpanjang sebagai kelulusan benchmark lama.

Eksperimen tersimpan di `artifacts/verification/20261009-llama-extraction/`:
`run.py` timeout CPU; `run_examples.py` menghasilkan repetition/truncation;
`run_contiguous.py` selesai tetapi 10/15 locator invalid dan tanpa assertion.
`run_literal.py` menguji source dengan newline literal dan selesai dalam 219,25
detik dengan `finish_reason=length`, cap 4096 habis; output terpotong, tidak valid
untuk diterima. Tidak ada prompt/input format eksperimen yang dipromosikan ke
runtime. Semua run tersebut telah terminal pada checkpoint ini; tidak ada
generation eksperimen yang perlu ditunggu. Periksa ulang jika ada run berikutnya.
File request saja tidak membuktikan proses masih hidup atau telah selesai.

## Pekerjaan B — resolusi dan kesiapan bahan graph

**Input:** ExtractionBatch valid, candidate policy per corpus, registry revision,
serta konteks sumber/kandidat. **Output:** keputusan canonical dengan provenance
dan checkpoint RESOLVE yang dapat dipakai ASSEMBLE.

**Audit integrasi pada `c8b03c7`:** masalah model dan kelengkapan registry adalah
dua pekerjaan yang perlu dibuktikan terpisah. `RegisterCanonicalAliases` tersedia
di adapter PostgreSQL, tetapi pemanggil yang ditemukan dalam source Go masih
berupa tes. `BindingExecutionStore` dan executor BIND mengalokasikan identitas
exact tanpa mendaftarkan profil/alias tersebut sebagai kandidat RESOLVE.
Karena itu, lookup alias kosong **belum membuktikan entitas baru**. Pergantian
model tidak menutup celah wiring ini dengan sendirinya.

Sebelum menambah CREATE otomatis, sambungkan pengisian profil/alias bersumber
untuk identitas yang memang sudah ada. Pertahankan authority BIND melalui
`PlanDocumentRegistryDependencies` dan pemeriksaan registry terkait. Jangan
menganggap `SourceRefs.RegulationId` adalah entitas yang disebut sebuah mention:
field itu dapat menunjuk dokumen yang memuat rujukan ke regulasi lain. Teks sama
atau lokasi di dokumen yang sama juga bukan bukti identitas.

Perhatikan kontrak evidence saat menentukan implementasi: reader
`LoadResolutionEvidenceSources` saat ini mencari support **mention EXTRACT**
dari checkpoint sukses. Alias dari judul/metadata BIND tidak otomatis memiliki
support yang bisa dihidrasi reader itu. Jangan membuat ID mention fiktif agar
lookup terlihat lengkap. Jalur dukungan metadata/struktur memerlukan integrasi
reader dan validator tersendiri, atau gunakan mention EXTRACT terverifikasi
dengan keputusan pemetaan canonical yang eksplisit. Pengujian harus membuktikan
alias terdaftar dapat menjadi kandidat, konteks sumbernya terbaca, dan kasus
lintas corpus/scope serta revision stale ditolak. Temuan ini adalah backlog
integrasi, bukan klaim implementasi atau acceptance yang sudah selesai.

1. Bekukan `scopes_by_type` untuk tipe yang benar-benar dikeluarkan extractor.
   Jangan menyamakan nomor regulasi tanpa issuer/type/year, atau alias sama dengan
   entitas sama. Konfigurasi domain yang ambigu perlu dikonfirmasi kepada pengguna.
2. Jalankan gateway RESOLVE terpisah dan coordinator. Model menilai LINK/DEFER;
   commit dan audit tetap milik Go/PostgreSQL. Review operator harus benar-benar
   membaca konteks; jangan membuat alasan persetujuan manusia secara fiktif.
3. Periksa entitas baru/tanpa kandidat. BIND sudah dapat membuat identitas
   regulasi/provision dari sumber, tetapi itu tidak berarti semua entitas semantik
   mempunyai canonical. Tutup kekurangan jalur CREATE yang diperlukan corpus
   melalui workflow/registry yang teruji; jangan menyisipkan row manual atau
   meluluskan DEFER agar publication terlihat siap.
4. Tangani registry revision yang berubah menggunakan reaffirmation yang sudah
   tersedia bila konteks masih identik, atau replan bila dependency berubah.

Kode utama: `src/server/internal/workflows/semantic_resolution*.go`,
`semantic_review.go`, `src/server/internal/adapters/postgres/registry_semantic*.go`,
`src/ingestion/src/knowledge_graph/resolution/` dan `assembly/`.

[Resolusi](semantic-resolution.md), [review](semantic-review.md), dan
[reaffirmation](graph-reaffirmation.md) menjelaskan kontrak serta cara menjalankan.
Dokumen lama yang menyebut ASSEMBLE belum ada bersifat historis; implementasi dan
[preparation graph](graph-preparation.md) kini tersedia.

**Syarat selesai B:** seluruh sumber yang akan diterbitkan memiliki keputusan
yang dapat dieksekusi, review yang diwajibkan, revision/scope yang valid, serta
bukti sumber. Empty/DEFER/status STAGED saja tidak membuktikan graph siap.

## Pekerjaan C — indeks, assembly dan publication

**Input:** seluruh CHUNK terpilih, model embedding, dictionary/statistics dan
hasil RESOLVE. **Output:** snapshot published dengan indeks Qdrant dan graph
Neo4j yang memiliki receipts serta dapat dibaca kembali.

Urutan operasional:

1. Jalankan native inference dengan model/tokenizer/manifests yang sudah diverifikasi.
2. `prepare-snapshot` memilih source job + artifact CHUNK yang terdaftar.
3. Jalankan vocabulary → `prepare-dictionary` → freeze statistics atas **populasi
   yang sama**, kemudian `prepare-index` dan worker INDEX sampai semua child STAGED.
4. `publish-index` mengaktifkan indeks dense/BM25 setelah readback dan receipts.
5. Setelah seluruh RESOLVE siap, jalankan `prepare-graph` dengan parent snapshot
   indeks yang benar, worker ASSEMBLE sampai seluruh child STAGED, lalu `publish-graph`.

Perintah nyata beserta placeholders berada di [README utama](../README.md#running-the-local-graphrag-pipeline),
[native inference](native-inference.md), [lexical population](lexical-population.md),
[index publication](index-source-publication.md), [prepare graph](graph-preparation.md),
dan [publish graph](graph-publication.md). Placeholder harus diganti identitas dari
output command/registry; jangan menebak hash, snapshot ID atau source artifact.

Kode utama: `src/server/internal/indexing/`, `src/server/cmd/cli/`,
`src/ingestion/src/indexing/`, `src/ingestion/src/knowledge_graph/assembly/`,
serta adapter PostgreSQL/Qdrant/Neo4j.

**Syarat selesai C:** manifest sesuai corpus/scope/model, semua child lengkap,
backend readback dan receipts valid, active snapshot terbit, serta query benar-benar
membaca snapshot itu. PDF ada, job scheduled, atau node terlihat di Neo4j belum
cukup. Publication gagal tidak boleh mengganti pointer aktif dengan hasil parsial.

## Pekerjaan D — akses aplikasi dan operasi lokal

Bagian ini mendokumentasikan kebutuhan agar hasil pipeline dapat digunakan untuk
tanya-jawab. Ini pekerjaan operasional, bukan evaluasi gold atau benchmark;
keberadaannya dalam panduan tidak berarti stage ini sudah lulus.

1. Uji `query-evidence` pada snapshot nyata: mulai profil hybrid, lalu graph dan
   hybrid-graph. Periksa teks, version/source refs, ranking provenance dan paths.
2. Pasang generator GENERATE terpin, lalu jalankan `-answer`. Jalur answering lokal
   sekarang memerlukan llama.cpp dengan counting/admission; keberhasilan EXTRACT
   pada Ollama tidak otomatis membuat konfigurasi answering valid.
3. Uji pertanyaan berbukti, bukti tidak cukup, filter tanggal, serta backend gagal.
   Citation harus menunjuk evidence yang benar; PARTIAL/ABSTAIN/error harus eksplisit.
   Ini smoke fungsional, bukan klaim akurasi atau kualitas hukum.
4. Jalankan API dengan corpus/auth scope/token dan model yang sama. Dokumentasikan
   readiness, request, restart dan shutdown. Jika UI produksi dibutuhkan, sambungkan
   ke endpoint ini; jangan menyebut UI demo BM25 sebagai UI Hybrid GraphRAG.

```powershell
# DSN, shared artifact root, corpus/scope, native model dan graph route harus sudah siap.
go run ./src/server/cmd/cli query-evidence -question 'Apa ketentuan yang diubah dalam peraturan ini?' -as-of 2026-01-01 -profile hybrid-graph -unresolved report -limit 20 -timeout 5m
# Sesudah REGULAGRAPH_ANSWER_CONFIG dan hash/config generator valid:
go run ./src/server/cmd/cli query-evidence -question 'Apa ketentuan yang diubah dalam peraturan ini?' -as-of 2026-01-01 -profile hybrid-graph -unresolved report -limit 20 -timeout 5m -answer
```

Pertanyaan/tanggal di atas contoh, bukan gold answer atau tanggal berlaku yang
disahkan. Pilih pertanyaan sesuai dokumen. [Query](query-evidence.md),
[jawaban lokal](local-answer.md), [API](evidence-api.md), dan
[batas token](generator-tokenization.md) adalah referensi konfigurasi.
Kode: `workflows/answer.go`, `workflows/rag_hydration.go`, `retrieval/`,
`answering/`, `cmd/cli/query.go`, dan `cmd/api/` di `src/server`.

**Syarat selesai D:** operator lain dapat menjalankan ulang dari konfigurasi dan
inventory tercatat, mendapat evidence/jawaban dari corpus nyata, serta mematikan
dan melanjutkan layanan tanpa menghapus data. Belum ada klaim target latency.

## Persiapan dan catatan yang wajib disimpan

Semua contoh dijalankan dari root repo. `.env.example` adalah referensi; executable
tidak otomatis memuat `.env`. Jangan memasukkan API key/DSN berpassword ke laporan
atau Git. Layanan lokal masih memerlukan credential backend bila dikonfigurasi.

Simpan satu manifest run operasional yang mencatat revision kode, corpus/auth scope,
source job dan artifact refs, snapshot/publication/generation, path artifact root,
model/prompt/schema/ontology/policy hashes, endpoint nonrahasia, perintah, exit code
dan lokasi log. Saat ini belum ada manifest full-PDF → graph → answer yang lulus;
agent berikutnya harus membuatnya dari output nyata, bukan mengisi tebakan.

```powershell
git status --short
git log -5 --oneline
# Terapkan migration pada DB yang memang dipilih; termasuk 0024 untuk EXTRACT replay.
go run ./src/server/cmd/cli migrate -dir migrations -timeout 5m
```

Urutan layanan: PostgreSQL/Qdrant/Neo4j → model + native inference → gateway
EXTRACT/RESOLVE → Rust worker → Go coordinator → API. Worker dan Go harus memakai
artifact store yang sama. Jangan mengaktifkan print-producer saat bermaksud
menyalakan gateway; mode itu mencetak konfigurasi lalu keluar.

Saat berhenti, gunakan Ctrl+C untuk proses foreground. Untuk background, verifikasi
PID **dan path executable**, lalu hentikan hanya proses milik run. Jangan mematikan
semua Go/Ollama/Docker. Saat lanjut, periksa proses/job nyata; jangan menyimpulkan
masih berjalan dari file `.pid`, log lama, atau nama folder saja. Timeout pengamatan
bukan bukti task sudah terminal. Jangan mengirim ulang inference sebelum memastikan
task lama selesai/dibatalkan.

## Urutan kerja agent penerus

1. Baca `AGENTS.md`, dokumen ini dan checkpoint terbaru; periksa worktree sebelum edit.
2. Periksa hasil/handle eksperimen terakhir. Jika masih hidup, tunggu/pantau handle
   yang sama. Jika terminal, simpan hasil dan tentukan perubahan berdasarkan bukti.
3. Jangan mengulang implementasi durable completion replay atau graph publication
   yang sudah ada. Fokus pada input nyata dan gap yang terbukti.
4. Pilih paket koheren, implementasikan, jalankan tes sesuai perubahan. Untuk
   boundary kritis gunakan reviewer independen sesuai [verification](verification.md).
5. Update dokumen ini pada checkpoint, simpan raw evidence di `artifacts/verification`,
   commit per fitur/komponen dengan pesan jelas; tidak memakai ID milestone saja
   dan tidak menambah coauthor OpenAI. Push ke origin sesuai otorisasi sebelumnya.

Untuk perubahan dokumentasi saja, periksa tautan/perintah/status; tidak perlu
menjalankan ulang semua model. Untuk implementasi, fixture PASS tidak menggantikan
run sumber/model nyata. Jangan mengklaim aplikasi siap sebelum A–D dan operasi
yang dipilih terbukti, atau proyek selesai sebelum seluruh requirement awal lulus.

## Setelah versi operasional tersedia

Rencana lengkap tetap mencakup gold evaluation dan empat baseline, benchmark
required, adaptive routing/retrieve ulang, temporal CURRENT/COMPARE, streaming,
incremental update lengkap, OCR/layout sulit, canonical lifecycle, observability,
backup/restore dan retention/GC. Lihat [rencana keseluruhan](development-plan.md).
Daftar ini bukan alasan menunda integrasi versi operasional; juga bukan fitur
yang boleh dinyatakan selesai hanya karena happy path lokal berhasil.
