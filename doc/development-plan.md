# Rencana implementasi berbasis dependency

Dokumen ini memecah desain lengkap RegulaGraph-ID menjadi pekerjaan komponen dan integrasi yang dapat diverifikasi. Perannya menjaga cakupan seluruh produk sambil mengurutkan pekerjaan menurut dependency nyata. Status terkini: D01 berjalan sebagian; C01/E01 dan fondasi storage/publication S01 tersedia; I01 memiliki handoff source observation terikat blob, library PDFium sampai persistence `DocumentBatch`, incremental planner, selector timeline, serta worker/coordinator durable PARSE/STRUCTURE/CHUNK. K01 telah memiliki planner exact regulation identity, allocator PostgreSQL revisioned/idempotent, materializer registry-bound regulation/provision, dan executor BIND durable. CHUNK memakai tokenizer Hugging Face hash-pinned dan binding node-versi eksak, tetapi parity model serta benchmark belum diukur. M01 secara keseluruhan, resolusi semantik dan merge/split, stage EXTRACT–INDEX, extraction change-event, serta full-rebuild equivalence tetap belum selesai; tabel menetapkan hasil yang harus dicapai, bukan klaim kelulusan seluruh paket.

## 1. Prinsip pelaksanaan

Status D01 diperbarui: [collector dan audit PDF](acquisition.md) sudah memverifikasi integrity manifest 616 record, 619 observation, serta 650 PDF unik/2.999.240.002 byte dari batch BPK/Kemkomdigi dan percobaan JDIHN. Inventory seluruh corpus, connector JDIHN, 2.545 URL queue pending, 526 missing document reference, klasifikasi gold format, dan gold dataset belum selesai. C01, E01, serta control-plane S01 sudah direalisasikan. I01 mengikat metadata portal ke source blob sebagai assertion, memverifikasi hash/identity, menghasilkan teks/locator/status halaman, mapping byte canonical, hierarchy structure, parent-aware chunk, reference closure, persistence content-addressed, incremental plan, serta timeline as-of. Worker gRPC dan coordinator Go menjalankan handoff PARSE→STRUCTURE→BIND→CHUNK dengan deadline/fence/cancellation, artifact/checkpoint binding, dependency evidence, terminal outcome, retry, dan crash recovery per stage. OCR, tabel, extraction change-event, resolusi issuer/semantic merge-split, temporal/structure gold, stage EXTRACT–INDEX, full-rebuild equivalence, RSS, parity tokenizer/model, dan benchmark produksi tetap belum diukur.

Folder tree menentukan pemilik kode; dependency menentukan urutan pelaksanaan. Satu milestone dapat mengubah contracts, Rust, Go, inference, dan evaluation bersama. Implementasi komponen yang terpisah dapat dikerjakan paralel setelah kontrak terkait stabil, tetapi setiap integrasi mempunyai satu owner hasil dan syarat selesai. Dokumen ini bukan instruksi menambah microservice atau agent kerja otomatis.

Perancangan seluruh kontrak, skenario sukses/gagal, storage, dan evaluasi dilakukan sebelum fitur produksi dikembangkan. Pembuktian model/engine serta bentuk data menggunakan eksperimen terukur. Komponen yang belum tersedia harus menghasilkan status belum diimplementasikan, bukan mock yang dianggap hasil produksi. Runtime/config/codegen yang ditambahkan nanti memperbarui header/README sesuai keadaan sebenarnya.

## 2. Work packages dan dependency

| ID | Paket pekerjaan lengkap | Bergantung pada | Pemilik utama | Hasil dan syarat selesai |
| --- | --- | --- | --- | --- |
| D00 | Review desain menyeluruh | Tidak ada | doc | Semua UC01-UC08 memiliki kontrak, owner, failure path, persistence, dan acceptance mapping; keputusan terbuka punya cara pembuktian |
| D01 | Inventory BPK/Komdigi/JDIHN dan audit dokumen | D00 | sources + data | Manifest sumber, format, mirror, versi, missing refs, konflik, serta rencana corpus sesuai workload |
| C01 | Seluruh kontrak wire/domain/evaluation | D00 + sampel D01 | contracts + domain + datasets | Semua record/RPC/event dalam system-contracts diterjemahkan ke schema; DAG import valid; codegen dan compatibility fixtures lintas bahasa lolos |
| E01 | Harness evaluasi dan telemetry | C01 | evaluation + Go config | Loader YAML, validasi manifest/workload, raw observations, gate evaluator, trace, status PASS/FAIL/BLOCKED/NOT_MEASURED; tidak meluluskan sampel kosong |
| S01 | Storage model dan publication foundation | C01 | migrations + Go adapters/workflows | Constraints, immutable artifacts, operation ledger, snapshot filtering/pinning, idempotency, crash/abort recovery terhadap DB nyata |
| M01 | Pembuktian engine dan model | D01 + E01 | tooling + inference + parsing | Matriks PDF/OCR, model/tokenizer/backend/precision, export/parity, quality/latency/memory; pilihan dipin beserta hasil |
| I01 | Akuisisi dan dokumen terstruktur | C01 + S01 + pilihan parser M01 | Go sources + Rust document | Teks/scan/tabel, mapping, struktur, parent chunk, versi/change events, quarantine; parsing/chunk gates dapat dijalankan |
| G01 | Gold dataset lengkap | D01 + C01; diperkaya I01 | evaluation/datasets | Human-reviewed parsing/graph/query gold, split groups, evidence alternatif, frozen manifests memenuhi workload |
| K01 | Graph engineering | I01 + S01 + semantic model M01 | Rust knowledge_graph + Go registry | Extraction, blocking/resolution, reversible decisions, qualifiers/supports, assembly/profiles; graph quality/invariant checks |
| N01 | Native inference produksi | C01 + M01 | C++ inference + Go/Rust adapters | Server entry point, warm sessions, tokenization parity, batch/result correlation, priority/queue/cancel, load tests |
| X01 | Indeks dense dan BM25 versioned | I01 + N01 + S01 | Rust indexing + Go Qdrant | Analyzer/dictionary/statistics generations, vector compatibility, native visibility filters, incremental/full equivalence |
| Q01 | Retrieval lengkap | K01 + X01 + N01 + E01 | Go retrieval | Query linking, parallel branches, path completion, filters, fusion, reranker, evidence endpoint; retrieval gold & load tests |
| A01 | Grounded answering | Q01 + M01 + G01 | Go answering/API | Parent/path context, generator, streaming/cancellation, claim/citation mapping, temporal/conflict/abstention; answer metrics |
| U01 | Incremental closure dan recovery menyeluruh | I01 + K01 + X01 + S01 | Go update + Rust change_detection | Dependency positif/negatif, selective reuse, registry merge/split impact, replay, partial failures, full-rebuild equivalence |
| O01 | Packaging dan operasi | S01 + N01; diselesaikan setelah A01/U01 | deployment + scripts | Reproducible images/models, auth/roles, readiness, migrations, graceful shutdown, backup/restore drill, resource isolation |
| B01 | Acceptance dan optimasi seluruh sistem | E01 + G01 + A01 + U01 + O01 | evaluation + semua owner | Empat baseline, ablations, seluruh 59 gate applicable, tiga run valid, raw artifacts; semua required lulus pada kondisi referensi |

G01 dan D01 berjalan sepanjang pengembangan; label test tidak dipakai untuk tuning. S01 membuktikan publication dengan fixture sebelum pipeline model lengkap, kemudian U01 membuktikannya lagi dengan dependency nyata. Ini pembuktian bertahap terhadap desain penuh, bukan penundaan desain konsistensi hingga akhir.

## 3. Pemecahan per komponen

| Komponen | Subkomponen dan hasil yang harus dapat ditinjau |
| --- | --- |
| src/contracts | common identity/presence/errors; documents hierarchy/temporal; graph canonical/assertions/support; evidence index/retrieval; answers stream/claims; jobs ledger/publication; inference batch/gateway |
| src/server/internal/api | Route/schema, auth/scope, validation, deadlines, streaming terminal behavior, health/readiness |
| src/server/internal/workflows | Ingest/update/query coordination, durable job state, publication, cancellation dan recovery |
| src/server/internal/retrieval | Query normalization/linking/classification, lexical/dense/graph, fusion/filters/reranking, completeness reporting |
| src/server/internal/answering | Parent/context assembly, generator adapter, claim/citation mapping, final validation dan abstention |
| src/server/internal/adapters | PostgreSQL metadata/ledger, Qdrant vectors/filters, Neo4j traversal/support, artifacts, worker/inference clients |
| src/ingestion/src/document | PDF/HTML/OCR, mapping, normalization, structure/parent chunks, provision versions dan change detection |
| src/ingestion/src/knowledge_graph | Schema/ontology, extraction validation, aliases/blocking/resolution, assembly, profiles, graph validation |
| src/ingestion/src/indexing | Dense batch dan BM25 tokenization/weights, dictionary/statistics compatibility, manifest dependencies |
| src/inference | Runtime/model lifecycle, embeddings, cross-encoder, batch scheduler, RPC wrapper dan parity |
| evaluation | Gold schemas, profiles/ablation, metrics, runner/gates, manifests dan raw result reports |
| tooling/corpus | Sampling inventory dan pembanding parser/OCR offline yang mengikat input/config/result hash |
| tooling/models | Model inventory/export, reference parity, tokenizer/pooling/precision checks |

Komponen yang memerlukan file baru ditempatkan menurut README scope yang sudah disetujui. Desain penuh tidak mengharuskan satu file placeholder untuk setiap message atau helper. Setiap commit menjelaskan fungsi yang benar-benar tersedia dan pengujiannya, tanpa mengklaim pipeline selesai ketika hanya schema/build yang tersedia.

## 4. Pemetaan benchmark ke pemilik

Angka dan definisi lengkap tetap hanya di [benchmark-targets.yaml](../configs/benchmark-targets.yaml). Tabel ini menghubungkan keluarga gate ke pekerjaan dan data; bukan salinan ambang.

| Gate/prefix | Paket owner | Instrumen dan bukti |
| --- | --- | --- |
| QUERY.* | Q01/A01/B01 | Load generator open-loop, scheduled arrivals, substantive TTFT, completion, inter-token, timeout/error |
| GO.* | E01/Q01/A01 | Exclusive span overhead tanpa backend wait, process RSS/heap/CPU |
| SEARCH.* | X01/K01/Q01 | Backend client spans termasuk queue/network; corpus/generation pin |
| MODEL.* | M01/N01 | Tokens/shape/batch, queue time, end-to-end client latency, throughput dan VRAM |
| CONTEXT.* | A01 | Batch parent/path fetch, tokenization dan context construction spans |
| QUALITY.* | G01/Q01/A01 | Human gold, acceptable evidence sets, slice labels, semantic citation/answer judgments |
| PARSING.* | D01/G01/I01 | Format-stratified pages, structure/CER/critical-token labels, service/aggregate time |
| CHUNKING.* / RUST.* | I01 | Preparsed transform workload, source mapping coverage, combined worker RSS |
| EXTRACTION.* / RESOLUTION.* | G01/K01 | Relation/support labels, same/different pairs, candidate blocking coverage |
| GRAPH.* | K01/S01 | In-memory assembly terpisah dari database acknowledged commit |
| UPDATE.* | U01/S01 | Change events, readiness/publication timing, full-rebuild comparison dengan manifest sama |
| ISOLATION.* | N01/O01/B01 | Simultaneous query+ingestion, absolute limits dan ratios; ingestion tidak dihentikan |
| INVARIANT.* | C01/S01/I01/K01/U01 | Seluruh record/fixtures relevan: source, orphan, snapshot, replay, wire, unchanged reuse |
| PARITY.* | M01/N01 | Model native versus reference pada dataset frozen; absolute quality juga berlaku |

Dataset dan workload yang belum siap menghasilkan BLOCKED/NOT_MEASURED, bukan nilai nol yang diluluskan. E01 harus menguji evaluator dengan kasus boundary, denominator kosong, NaN/infinity, missing samples, rejected arrivals, run gagal, manifest mismatch, dan gate yang tidak berlaku. Ini pengujian perilaku evaluator, bukan test yang sekadar mencocokkan daftar 59 nama.

## 5. Skenario integrasi yang wajib dirancang sebelum coding

| ID | Skenario | Expected behavior |
| --- | --- | --- |
| T01 | PDF sama ditemukan pada dua portal | Blob reuse, dua observations, satu identitas regulasi terverifikasi |
| T02 | Nomor/tahun sama, issuer/type berbeda | Tidak di-merge sebagai regulasi sama |
| T03 | Unicode, tabel, footer dan nomor pasal | Mapping UTF-8, struktur, reading order dan token kritis terjaga |
| T04 | Scan sebagian halaman gagal | Partial/karantina dilaporkan; tidak mempublikasikan dokumen lengkap palsu |
| T05 | Perubahan hanya satu ayat | Versi bagian terdampak berubah; historical query tetap menemukan versi lama |
| T06 | Tanggal berlaku unknown atau konflik | Tidak ditebak dari tanggal fetch; uncertainty tampil pada hasil |
| T07 | Alias sama untuk dua objek | Kandidat tetap terpisah hingga resolution memiliki evidence |
| T08 | Merge salah kemudian split | Mention/support dapat direkonstruksi sesuai registry revision |
| T09 | Satu dari beberapa support dihapus | Assertion tetap bila support sah lain ada |
| T10 | Pengecualian di dokumen lain | Evidence/context mencakup syarat dan exception; tidak menjawab aturan umum saja |
| T11 | Context/token/hop budget habis | PARTIAL/abstain/clarify terstruktur; completeness tidak dipalsukan |
| T12 | Backend/model gagal atau kapasitas penuh | Error/deadline dihitung, bukan query tanpa jawaban yang benar |
| T13 | Generation gagal setelah teks keluar | Stream mendapat error terminal; provisional text bukan final sukses |
| T14 | Query berlangsung saat snapshot baru publish | Parent, evidence, graph dan citation memakai snapshot yang dipin |
| T15 | Crash sebelum/sesudah publication CAS | Recovery mengikuti committed state tanpa duplicate/partial visibility |
| T16 | Abort setelah closure visibility | Closure dipulihkan sebelum publication selanjutnya |
| T17 | Lease lama mengirim hasil terlambat | Fence/expected revision menolak hasil dan menahan unsafe writer takeover |
| T18 | Sumber/dependency identik diulang | Tidak ada extraction model ulang; logical replay identik |
| T19 | Dokumen baru mengisi lookup yang sebelumnya kosong | Dependency negatif menginvalidasi consumer yang relevan |
| T20 | Model/tokenizer/BM25 statistics berubah | Generation baru divalidasi; query tidak mencampur representasi |
| T21 | Reader lama bersamaan garbage collection | Retention/read lease melindungi semua artefak yang direferensikan |
| T22 | Restore tiga backend dari versi berbeda | Readiness ditolak sampai manifest konsisten |
| T23 | Typo/informal/code-switch | Intended evidence dan tanggal dipertahankan, diuji per slice |
| T24 | Parafrasa satu pertanyaan masuk dua split | Dataset validation menolak kebocoran group |

Fixture deterministik untuk T01-T24 dilengkapi corpus nyata dan pengujian model; seluruh skenario bukan pengganti required benchmark skala penuh. Expected outcomes disahkan dari kontrak sebelum implementasi sehingga test tidak sekadar meniru kode.

## 6. Review dan definisi selesai tiap paket

Paket selesai ketika implementasi nyata memenuhi kontrak, dokumentasi status diperbarui, test bermakna dan benchmark applicable dijalankan, serta hasil/raw manifest tersimpan. Jalur sukses, malformed input, cancellation, partial failure, dan recovery relevan harus diperiksa. Optimasi yang mengubah representation/candidate coverage diuji kembali kualitasnya.

Review menanyakan siapa owner state, apakah dependency/ID/version dipertahankan, bagaimana error terlihat, berapa overhead tambahan, dan bagaimana perubahan dibuktikan. Commit dibagi per komponen atau kemampuan integrasi dengan deskripsi konkret. Commit/push tidak boleh dinyatakan selesai hanya karena remote telah terpasang; verifikasi local HEAD dan remote main setelah push.

Jika required gate gagal, lakukan profiling, perbaikan dan uji ulang. Jangan menurunkan workload, menghapus kasus sulit, atau menunda gate dari kriteria release untuk memperoleh PASS. Jika ingin mengubah benchmark, ajukan perubahan konkret dan dampaknya kepada pengguna; selama belum disetujui standar lama berlaku.

## 7. Titik lanjut setelah document artifact boundary

Kerja berikutnya berjalan pada tiga jalur yang saling memasok bukti: tuntaskan D01 inventory/audit sumber, bangun G01 gold structure/temporal, dan lanjutkan output CHUNK durable ke extraction change-event/EXTRACT. Terminal outcome PARSE/STRUCTURE/BIND/CHUNK sudah durable dan crash sesudah checkpoint dapat direkonsiliasi tanpa menganggap batch parsial sukses; scheduler selanjutnya memerlukan lease heartbeat sebelum batch panjang. OCR per halaman, tabel, exception linking, serta parity tokenizer BGE-M3 tetap perlu dibuktikan. M01 memilih OCR, embedding, reranker, tokenizer, serta backend native berdasarkan kualitas, latency, throughput, dan memori.
