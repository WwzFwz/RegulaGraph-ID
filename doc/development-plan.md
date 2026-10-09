# Rencana implementasi berbasis dependency

Checkpoint quote EXTRACT v2: schema/prompt baru memungkinkan kutipan plus konteks
langsung; gateway menghitung unique exact UTF-8 offsets dan tetap memakai gate
provenance/ontology/C01 lama. Unit, suite Go dan review boundary lulus. Model nyata
masih gagal 4 dari 10 locator pada satu chunk; seluruh item ditolak. Berikutnya
perbaiki evidence copying/model feedback, token admission dan per-item recovery.
[Bukti dan batas](verification-report-quote-extraction.md).

Checkpoint completion budget: EXTRACT/RESOLVE memakai cap output eksplisit dan
producer pin; actual config/input hashes diperiksa sebelum acceptance EXTRACT.
Suite Go dan review independen lulus. Satu chunk model lokal selesai dengan 1064
token output, tetapi seluruh 9 span masih invalid. Berikutnya bangun quote-to-source
alignment deterministik serta admission full prompt; belum PASS extraction.
[Bukti dan batas](verification-report-semantic-budget.md).

Checkpoint bootstrap/EXTRACT: CLI `migrate` kini menerapkan schema aplikasi dengan
checksum replay, transaksi per file dan cleanup lock berbatas. Model menerima
ontology context terpin; missing/drift pin ditolak coordinator, konfigurasi dimiliki
service, dan context masuk byte budget. Review/regresi independen lulus mekanismenya.
Uji EXTRACT PDF nyata tetap gagal deadline; observer menunjukkan output limit profil
demo dan kesalahan span/relasi model. Lanjutkan perbaikan profile/evidence representation
serta checkpoint item, bukan menurunkan gate. [Laporan](verification-report-runtime-bootstrap.md).

Checkpoint PDF nyata: impor PDF BPK kini teruji sampai PARSE, STRUCTURE, BIND dan
CHUNK dengan PDFium serta tokenizer BGE aktual; 35 chunk mempertahankan sumber,
struktur, token accounting dan provision version. Metadata policy v2 memisahkan
kategori katalog/bentuk regulasi; parser structure-v2 menerima marker standalone,
pasal Romawi dan catchword terkonfirmasi. EXTRACT/model sampai publication PDF ini
masih berikutnya. [Bukti dan kompatibilitas](verification-report-real-pdf.md).
README utama kini memuat PlantUML, kronologi komponen, alasan teknologi, struktur
folder lengkap, operasi demo/GraphRAG serta rebuild tanpa menghapus sejarah.

Checkpoint impor sumber: `submit` kini menerima complete acquisition record,
memverifikasi/copy seluruh PDF ke storage bersama, meregistrasi refs corpus-scoped
dan menyimpan observations pada job PARSE. PDF download nyata sudah diuji sampai
enqueue/replay pada PostgreSQL terisolasi; parsing/model sampai publication corpus
nyata tetap langkah berikutnya. [Kontrak](acquisition-import.md).

Checkpoint reaffirmation: sumber RESOLVE dari revision berbeda kini dapat disusun
pada satu target retained view melalui [policy eksplisit](graph-reaffirmation.md).
Keputusan/proposal/model asli dipertahankan; historical ledger, seluruh candidate
context dan BIND identities diperiksa, lalu receipt disimpan dengan registry stamp
di bawah lock. Native Rust/publication/retrieval teruji pada fixture, termasuk
frozen-view replay. Perubahan konteks tetap meminta replan; corpus/model/gold,
semantic/temporal query lengkap, streaming, incremental dan acceptance masih terbuka.

Checkpoint persiapan operator: [prepare-graph](graph-preparation.md) membaca semua
anggota snapshot indeks awal, memverifikasi RESOLVE, menyimpan envelope/plan dan
menjadwalkan child ASSEMBLE atomik. Executable preparation hingga Rust, publication
Neo4j/Qdrant dan retrieval/draft fixture teruji. Reaffirmation lintas registry
revision masih diperlukan untuk menggabungkan sumber yang di-resolve pada revision
berbeda; corpus/model/gold dan required performance acceptance belum selesai.
Checkpoint lama di bawah merupakan riwayat, bukan seluruh backlog aktif.

Checkpoint operator publication: `publish-graph` kini merangkai restore/admission,
manifest graph+index, Neo4j write/receipt, verifikasi reuse Qdrant dan active CAS.
Fresh publication serta retry executable memakai output Rust nyata sudah diuji;
source/semantic label fixture tetap sintetis. [Panduan](graph-publication.md).
Operator preparation/scheduling graph dari seluruh corpus masih perlu disambungkan;
streaming, incremental penuh dan gold/performance acceptance tetap terbuka.

Checkpoint HTTP jawaban: `/v1/questions` aktif secara opt-in; runtime generator
diadmit sekali saat startup dan dipakai lintas request/snapshot. Evidence dan
answer memakai satu lease sampai final admission, auth/capacity yang sama dan
output draft tanpa promosi status. Rust/PostgreSQL/Neo4j/Qdrant/Qwen nyata telah
melalui HTTP pada fixture sintetis. [Panduan](evidence-api.md).
Streaming, routing temporal/semantic query lengkap, operator corpus end-to-end,
gold/model quality dan required performance acceptance tetap terbuka.
Riwayat checkpoint berikut tidak semuanya merupakan backlog aktif.

Checkpoint jawaban lokal: admission GGUF/build/template/window dan profile config
terpin tersedia. CLI `query-evidence -answer` memakai session snapshot yang sama
sampai generation/final citation validation, tanpa fallback ketika model gagal.
Native Rust/PostgreSQL/Neo4j/Qdrant/CLI/Qwen lulus pada fixture sintetis; model
abstain pada pertanyaan graph tanpa dukungan cukup. [Panduan](local-answer.md).
API jawaban, corpus/model/gold acceptance dan optimasi target required tetap
terbuka; catatan checkpoint di bawah adalah riwayat, bukan semuanya backlog aktif.

Checkpoint generator lokal: counter llama.cpp memakai body chat yang sama dengan
generation. Uji Qwen GGUF nyata melewati packing konteks, full-prompt count/usage
parity dan cited draft UNREVIEWED/PARTIAL dari evidence sintetis. [Kontrak](generator-tokenization.md).
Admission model/server/template serta command/API jawaban operasional masih perlu
disambungkan; gold dan required performance belum diukur.

Checkpoint API graph: HTTP evidence menerima empat profil retrieval; graph-only
teruji terhadap Rust/PostgreSQL/Neo4j/Qdrant nyata tanpa proses embedding, dengan
data sintetis. Cache graph mengikuti snapshot dan menutup resource lama setelah
request pemakainya selesai. [Panduan API](evidence-api.md).
Generation jawaban operasional, semantic query disambiguation, corpus/model/gold
run dan required acceptance masih terbuka. Checkpoint berikut merupakan riwayat
kemajuan; daftar belum selesai pada checkpoint lama dapat telah ditangani di atas.

Checkpoint entrypoint graph: CLI `query-evidence` menerima graph/hybrid-graph
dengan route/policy byte-pinned; graph-only executable teruji tanpa native
endpoint terhadap snapshot nyata dengan data sintetis. Lihat
[penggunaan](query-evidence.md). API graph configuration, command generation,
corpus/model/gold run dan required acceptance masih terbuka.

Checkpoint query linking: exact-alias seed discovery dari pertanyaan kini memakai
registry snapshot-bound dengan audit key positif/negatif dan alternatif ambigu.
Native kedua profil graph memakai lookup PostgreSQL hingga draft. Lihat
[kontrak](query-entity-linking.md); API graph configuration, semantic query
disambiguation dan quality/performance acceptance tetap belum selesai.

Checkpoint Q01: admission graph, typed reader dan discovery traversal kini teruji
dengan output Rust aktual, scope/pin, cycle/alternate path serta corruption rejection.
Graph evidence hydration kini terhubung ke source lookup Qdrant dan autentikasi
PostgreSQL/artefak, dengan pin recheck, span coverage dan dependency partial eksplisit.
Lihat [kontrak bukti](graph-evidence.md) dan [verifikasi](verification-report-graph-evidence.md).
Renderer graph yang mempertahankan seluruh membership path kini terhubung ke
generator/citation/final validation; [kontrak](graph-context.md) dan
[verifikasi](verification-report-graph-context.md). Graph branch/fusion serta
factory empat profil kini teruji sampai cited draft dengan fixed seed fixture:
[kontrak](graph-fusion.md), [verifikasi](verification-report-graph-fusion.md).
Berikutnya semantic query disambiguation, konfigurasi API graph,
applicability/dependency resolution dan acceptance.

Checkpoint terbaru K01: [adapter Neo4j](neo4j-graph-store.md) menulis generation
additive terisolasi, mempertahankan shared support, memverifikasi inventory exact dan
melakukan seal. Output Rust aktual kini lulus Rust → PostgreSQL STAGED → Neo4j,
termasuk replay dan cold recovery; lihat [laporan](verification-report-neo4j.md).
[Collection/preparation graph committed](graph-publication-preparation.md) kini
menyatukan output STAGED lengkap dengan revalidasi source/projection dan authority;
checkpoint baru sesudah recovery membatalkan hasil prepared lama.
[Catalog/write-intent graph](graph-generation-catalog.md) kini immutable dan atomik;
writer menguji lost acknowledgement, retry dan exact proof pada backend nyata.
[Bukti](verification-report-graph-catalog.md) belum mencakup activation.
[Receipt graph dan final authority guard](graph-readiness.md) kini tersedia:
acknowledgement recovery serta cancellation/registry/checkpoint drift diperiksa.
[Pemakaian ulang indeks](index-reuse.md) kini menghasilkan receipt Qdrant dari
readback lengkap dan mengaktifkan snapshot graph+index. Reader/hydration menjaga
snapshot source asal; native integration menghasilkan draft bersitasi melalui
dense/BM25. [Bukti](verification-report-index-reuse.md) masih memakai model fixture.
Berikutnya operator preparation/scheduling/publication serta
graph-to-answer; K01/Hybrid GraphRAG serta acceptance belum selesai.

Checkpoint sebelumnya: integrasi nonempty sudah menjalankan allocator/alias/candidate,
dua LINK RESOLVE dengan intent/checkpoint PostgreSQL, preparation/inventory, RPC Rust
ASSEMBLE, validasi Go dan commit STAGED. Processor baru memulihkan output tanpa RPC
tambahan. EXTRACT, review manusia, BIND dan vektor awal masih fixture sintetis;
ini bukan quality acceptance. Lihat [bukti integrasi](verification-report-native-graph.md).
Berikutnya operator preparation/scheduling graph, Neo4j publication/readiness,
retrieval graph dan integrasi jawaban; K01/Hybrid GraphRAG keseluruhan belum selesai.

Checkpoint terbaru K01: [processor/executor ASSEMBLE](graph-job-execution.md) tersedia
bersama wiring daemon opt-in, scope-filtered claim, cold inventory restore/cache,
recovery checkpoint dan klasifikasi kegagalan permanen. PostgreSQL/Qdrant menguji
restore/cache/refresh/pin cleanup dan recovery output hilang menjadi FAILED. Actual
RPC Rust bersama PostgreSQL dengan sumber nonempty kini diuji pada checkpoint
di atas; publication Neo4j, retrieval graph dan acceptance tetap belum selesai.

Checkpoint terbaru K01: output ASSEMBLE yang telah diverifikasi dapat di-commit
melalui `VerifiedGraphOutput.Commit`: metadata artefak, dependency, checkpoint dan
STAGED atomik, dengan recheck authority di bawah lock dan recovery acknowledgement.
[Laporan commit](verification-report-graph-output-commit.md) membatasi bukti pada
workflow fixture Rust dan transaksi PostgreSQL/Qdrant. Daemon/restart-reclaim,
RPC nyata bersama PostgreSQL, source nonempty end-to-end, Neo4j dan acceptance
masih terbuka. Catatan di bawah merupakan riwayat kemajuan, bukan daftar status
terkini yang semuanya harus dikerjakan ulang.

Lanjutan K01: [dispatch/admission output](graph-output-admission.md) tersedia sebagai
library Go dari authority inventory ke RPC port dan GraphDelta terverifikasi. Projection
source/canonical/support/dependency diuji dengan fixture worker Rust aktual, termasuk
exception DAG dan normalisasi hash-v1. Commit checkpoint/STAGED/recovery, daemon,
integrasi RPC nyata bersama PostgreSQL serta Neo4j masih terbuka; lihat
[laporan](verification-report-graph-output.md). Ini belum completion K01 atau benchmark.

Lanjutan K01: [inventory ASSEMBLE](graph-job-inventory.md) kini memiliki preflight
source/receipt/view, admission dengan registry stamp di bawah lock, child jobs atomik,
replay dan claim terpisah. PostgreSQL/Qdrant menguji rollback, pool satu koneksi,
registry lock race dan cancellation/reclaim. Dispatch/output commit, source nonempty
end-to-end, reaffirmation lintas revision serta Neo4j publication tetap diperlukan.
[Laporan inventory](verification-report-graph-job-inventory.md) memisahkan hasil fixture
dan review independen dari acceptance release.

Boundary dispatch berikutnya kini mempunyai live authorization satu-statement dan
builder request empat role terikat claim/plan. Tes backend menolak publication takeover,
source cancellation/checkpoint hilang, expired reader pin dan claim palsu. Eksekusi RPC,
admission isi GraphDelta serta commit/recovery output masih langkah berikutnya.

Lanjutan K01: freshness kandidat RESOLVE kini tersambung ke preparation pada revision
hasil commit. Seluruh positive/negative scopes dibandingkan; EXTRACT dengan dependency
eksternal tak didukung ditolak. Receipt historis dan bytes asli tidak diubah.
[Laporan](verification-report-graph-resolution-view.md) mencatat regresi budget alias
yang ditemukan review. Durable reaffirmation lintas revision, admission/inventory,
dispatch dan publication Neo4j tetap diperlukan.

Lanjutan K01: preparation graph memeriksa exact BIND registry dependencies pada target
revision, menerima penambahan tak terkait dan menolak identitas yang berubah/ditutup.
Binder PostgreSQL nyata dan jalur publication/preparation PG/Qdrant lulus beserta
review independen; lihat [laporan BIND registry](verification-report-document-registry.md).
Candidate freshness/reaffirmation, inventory/dispatch dan Neo4j publication tetap terbuka.

Lanjutan persiapan K01: reader workflow merekonstruksi RESOLVE dari receipt committed,
dan `PrepareGraphAssembly` menyimpan plan/canonical view deterministik dari source
receipt. Library menahan unresolved actions, ontology drift dan revision berbeda.
Verifikasi independen tersedia di [laporan preparation](verification-report-graph-preparation.md).
Inventory/dispatch, dependency freshness/reaffirmation dan Neo4j publication belum selesai.

Lanjutan K01: persistence envelope/dependency dan receipt graph source kini tersedia,
dengan replay setelah crash/lost acknowledgement serta pemeriksaan ulang source/target
di bawah lock PostgreSQL. Tes backend nyata lulus; review independen telah dilanjutkan
sesudah fasilitas tersedia kembali (lihat laporan). Inventory/dispatch ASSEMBLE, dependency
freshness lintas revision dan publication Neo4j masih terbuka. Lihat
[kontrak coordinator](graph-assembly-coordinator.md) dan
[bukti pemeriksaan](verification-report-graph-source-receipt.md).

Lanjutan K01 2026-10-09: reader receipt RESOLVE historis dan authority awal source/
publication ASSEMBLE tersedia di Go/PostgreSQL. [Verifikasi](verification-report-assembly-authority.md)
mencakup corruption, stale fence, cancellation, parent/revision drift dan backend nyata.
[Rencana handoff coordinator](graph-assembly-coordinator.md) mencatat binding snapshot
serta dependency lintas revision yang diperlukan sebelum inventory/dispatch graph penuh.

Pembaruan K01 2026-10-09: [canonical view exporter dan plan ASSEMBLE](graph-assembly-inputs.md)
tersedia sebagai library PostgreSQL/Go/Rust; exact selection terikat revision/publication,
role input dan ontology diperiksa. [Verifikasi](verification-report-graph-assembly-inputs.md)
mencakup backend nyata dan kontrak empat bahasa. Worker ASSEMBLE kini mempersist delta/checkpoint dari plan. Persistence plan/receipt
coordinator serta Neo4j publication masih perlu disambungkan; K01 keseluruhan belum selesai.

Dokumen ini memecah desain lengkap RegulaGraph-ID menjadi pekerjaan komponen dan integrasi yang dapat diverifikasi. Perannya menjaga cakupan seluruh produk sambil mengurutkan pekerjaan menurut dependency nyata. Status terkini: D01 berjalan sebagian; C01/E01 dan fondasi storage/publication S01 tersedia; I01 memiliki handoff source observation terikat blob, library PDFium sampai persistence `DocumentBatch`, incremental planner, selector timeline, serta worker/coordinator durable PARSE/STRUCTURE/CHUNK/EXTRACT. K01 telah memiliki planner exact regulation identity, allocator PostgreSQL revisioned/idempotent, registry alias append-only dan lookup kandidat ambigu/negatif, materializer registry-bound regulation/provision, executor BIND durable, proposal extraction berbukti yang di-commit durable, serta ontology EXTRACT terpin lintas Go/Rust. CHUNK memakai tokenizer Hugging Face hash-pinned dan binding node-versi eksak. Alias bersumber, pemeriksaan support, proposal resolusi model, dispatch RESOLVE sampai WAITING_REVIEW, serta parity diagnostik embedding/reranker native telah tersedia. Review/resume remote, canonical baru/merge/split, ASSEMBLE/INDEX, extraction change-event, full-rebuild equivalence, kualitas gold, dan benchmark required tetap belum selesai; tabel menetapkan hasil yang harus dicapai, bukan klaim kelulusan seluruh paket.

Submilestone K01 writer PostgreSQL menyimpan receipt LINK/DEFER secara atomik dengan review terikat hash proposal dan artefak kandidat, CAS revision, replay deterministik, dan guard append-only. Handoff berikutnya mengklaim RESOLVE dari checkpoint EXTRACT sukses, membaca EXTRACT/kandidat melalui `ReadVerified`, lalu membuktikan ulang fence/checkpoint di dalam transaksi registry. Workflow kini menyiapkan artefak kandidat dari rencana scope exact terpin, menyimpan intent sebelum CAS dan output/checkpoint sesudahnya, memulihkan crash/reclaim, serta menyelesaikan EXTRACT tanpa mention tanpa registry write. Kandidat/review yang permanen stale mengakhiri job dan meminta replan; pembuatan job pengganti otomatis belum tersedia. Policy scope/normalisasi terpin, proposal model berbukti dua sisi, dan dispatch RESOLVE opt-in sudah tersambung sampai WAITING_REVIEW; review/resume lokal kini tersedia, sedangkan konfigurasi scope produksi dan review remote masih terbuka. Graph assembly dan quality/performance gold juga belum selesai. Lihat [laporan writer](verification-report-k01-semantic-registry.md), [laporan handoff](verification-report-k01-resolve-handoff.md), [laporan output](verification-report-k01-resolve-output.md), [laporan replan](verification-report-k01-resolve-replan.md), dan [laporan penyimpanan kandidat](verification-report-k01-candidate-storage.md).

## 1. Prinsip pelaksanaan

Pembaruan graph source 2026-10-09: transform envelope CHUNK-to-EXTRACT/RESOLVE dan
reader membership pada published index inventory tersedia serta diuji. Hasil model,
assignment dan revisi historis dipertahankan; diagnostic root refs diremap secara
terbatas. Persistence receipt graph dilanjutkan dalam pembaruan di awal dokumen;
dependency lintas revision, inventory/dispatch ASSEMBLE dan writer Neo4j masih terbuka. Lihat
[laporan verifikasi](verification-report-graph-source-envelope.md).

Pembaruan K01 2026-10-09: [review/resume lokal](semantic-review.md) tersambung dari
queue ke approval/intent atomik dan executor. Perubahan canonical, registry view
historis, ASSEMBLE/Neo4j serta acceptance gold/performance tetap diperlukan.

Pembaruan X01 2026-10-09: planner inventory CHUNK, persistence plan/dependency,
dispatch worker berbatas lease dan exact output-set admission tersedia sebagai library
Go. Integrasi planner/persistence sampai publication/hydration diuji dengan PostgreSQL
dan Qdrant nyata serta model fixture. Persistent inventory/child-job scheduling dan
producer statistik dari rendered corpus telah dilanjutkan: population Rust,
snapshot source binding, bootstrap operator, inventory durable dan daemon INDEX
opt-in kini tersambung sampai publication dense/BM25. Run corpus/native nyata,
incremental dan benchmark seluruh X01 masih diperlukan.
Lihat [kontrak writer](initial-index-writer.md); ini belum kelulusan seluruh X01.

Status D01 diperbarui: [collector dan audit PDF](acquisition.md) sudah memverifikasi integrity manifest 616 record, 619 observation, serta 650 PDF unik/2.999.240.002 byte dari batch BPK/Kemkomdigi dan percobaan JDIHN. Inventory seluruh corpus, connector JDIHN, 2.545 URL queue pending, 526 missing document reference, klasifikasi gold format, dan gold dataset belum selesai. C01, E01, serta control-plane S01 sudah direalisasikan. I01 mengikat metadata portal ke source blob sebagai assertion, memverifikasi hash/identity, menghasilkan teks/locator/status halaman, mapping byte canonical, hierarchy structure, parent-aware chunk, reference closure, persistence content-addressed, incremental plan, serta timeline as-of. Worker gRPC dan coordinator Go menjalankan handoff PARSE→STRUCTURE→BIND→CHUNK→EXTRACT dengan deadline/fence/cancellation, artifact/checkpoint binding, dependency evidence, terminal outcome, retry, dan crash recovery per stage. EXTRACT mengikat source version, model/prompt/ontology bytes, accounting, typed graph, exact mention bytes, dan UTF-8 evidence sebelum commit. OCR, tabel, extraction change-event, resolusi issuer/semantic merge-split, temporal/structure gold, penyelesaian review RESOLVE serta stage ASSEMBLE/INDEX, full-rebuild equivalence, dan benchmark produksi masih terbuka. Parity diagnostik tokenizer/model native sudah diuji; hasil tersebut tidak membuktikan kualitas gold atau workload produksi.

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

Arahan terbaru pengguna menempatkan anotasi gold lengkap G01 setelah alur kode X01, K01, Q01, A01, dan U01 tersambung. Dependency G01 pada tabel adalah syarat pembuktian kualitas/acceptance, bukan larangan mengimplementasikan komponen dengan fixture perilaku sebelum gold lengkap. Schema dataset, provenance, telemetry, tes invariants, dan audit D01 tetap tersedia selama coding. Setelah gold dibuat, tuning memakai dev dan label test tetap terpisah. Target serta syarat release tidak berubah. S01 membuktikan publication dengan fixture sebelum pipeline model lengkap, kemudian U01 membuktikannya lagi dengan dependency nyata.

Rencana implementasi berikut memuat fungsi usulan, kontrak input/output, validasi,
perhatian performa, serta pembagian commit. Rencana bukan bukti fitur telah aktif.

| Paket | Rencana konkret | Handoff antarkomponen |
| --- | --- | --- |
| X01 | [Indeks dense/BM25](x01-implementation-plan.md) | IndexGeneration/IndexBatch, Qdrant readiness, kandidat pada snapshot terpin. |
| K01 | [Resolusi dan graph berbukti](k01-implementation-plan.md) | Keputusan registry teraudit, GraphDelta, Neo4j readiness dan path support. |
| Q01 | [Retrieval hybrid](q01-implementation-plan.md) | Menggabungkan indeks X01 dan graph K01 menjadi EvidenceBundle beserta completeness. |
| A01 | [Answering dan streaming](a01-implementation-plan.md) | Mengubah bundle Q01 menjadi konteks, klaim/citation dan AnswerEvent yang tervalidasi. |

Urutan kerja utama X01 → K01 → Q01 → A01 → U01, lalu G01 dan acceptance B01.
Ini urutan integrasi, bukan larangan mengerjakan library independen lebih awal.
Indeks dan graph merupakan dua keluaran yang bertemu pada publication/read snapshot;
candidate expansion K01 tidak boleh membuat dependency melingkar dengan publication X01.
Deployment tetap di luar pekerjaan yang diminta saat ini.

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

Prioritas terbaru adalah implementasi X01 menggunakan native model yang sudah aktif, lalu penyelesaian K01 dan integrasi Q01/A01/U01. Inventory/audit D01 serta pencatatan bukti tetap menyertai pekerjaan; anotasi gold G01 lengkap dikerjakan sesudah alur kode tersambung sesuai arahan pengguna. Terminal outcome PARSE/STRUCTURE/BIND/CHUNK/EXTRACT sudah durable dan crash sesudah checkpoint dapat direkonsiliasi tanpa menganggap batch parsial sukses; scheduler selanjutnya memerlukan lease heartbeat sebelum batch panjang. OCR per halaman, tabel, exception linking, serta parity kualitas retrieval BGE-M3 tetap perlu dibuktikan. Tokenizer native telah cocok dengan reference pada kasus diagnostik dan boundary 8192 token; ini belum membuktikan kualitas gold. M01 memilih OCR, embedding, reranker, tokenizer, serta backend native berdasarkan kualitas, latency, throughput, dan memori.

Kemajuan X01: Rust kini merender span chunk dengan label induk serta node
pemilik secara berbatas, membaca pilihan chunk dari TextArtifact terverifikasi,
menghitung key reuse vektor yang mengikat corpus/policy/model, serta memvalidasi
satu batch dense native secara all-or-nothing.
Go memvalidasi closure lokal `IndexBatch`, menyediakan transport Qdrant dan
readback exact-ID, serta mensyaratkan payload index filter snapshot pada
collection yang di-bootstrap eksklusif. Batch dan Qdrant menolak ID term sparse
yang tidak terurut. `IndexGeneration.embedding_input_policy` kini dipin pada
schema, divalidasi Go/Rust, dan dicocokkan ke metadata collection; upgrade
semua route reader sebelum publication masih perlu dibuktikan. Allocator PostgreSQL
append-only, exporter snapshot dictionary typed, serta reader Go/Rust kini tersedia
sebagai library dengan fixture lintas bahasa. Binding registry/lineage otoritatif,
dispatch plan terpin dari allocator ke
worker INDEX, ledger mutasi, closure/recovery, readiness replica, dan
publication tetap terbuka. Lihat [rencana X01](x01-implementation-plan.md)
serta [verifikasi policy generation](verification-report-x01-input-policy.md).

Artefak analyzer/statistik BM25 typed kini diproduksi Rust dan dibaca loader Go
dengan pemeriksaan hash, corpus, exact base dictionary, formula serta populasi.
Factory retriever menolak encoder tanpa binding dan statistik dari snapshot masa
depan. Fixture parity dan Qdrant nyata sudah diuji; authority katalog dan dispatch coordinator
INDEX belum terhubung. Worker INDEX kini merakit output dari plan terpin; lihat [kontrak INDEX](index-build.md). Lihat [kontrak lexical](lexical-generation.md) serta
[laporan verifikasi](verification-report-lexical-generation.md).

## Kelanjutan K01: proposal model kontekstual

Gateway RESOLVE dan workflow `ProposeWithModel` tersedia untuk input kandidat/claim terverifikasi, dengan hidrasi konteks mention serta bukti kandidat lintas dokumen, alasan berbukti, audit immutable, dan replay. Katalog support EXTRACT disimpan atomik bersama checkpoint sukses; LINK wajib merujuk kedua sisi konteks. Planner kandidat exact lintas scope memakai policy terpin pada request ingest durable dan konfigurasi handoff tepercaya, mencakup seluruh tipe ontology v1 tanpa memangkas scope ambigu. Loader policy serta CLI submit sekarang mem-pin hash ontology/policy dan telah diuji terhadap PostgreSQL nyata; CLI menerima referensi source blob karena dispatcher ACQUIRE belum aktif. Pemanggil menyiapkan blob terdaftar; pemeriksaan keberadaan dan integritasnya terjadi downstream. Daemon kini dapat menjalankan RESOLVE secara opt-in sampai proposal durable WAITING_REVIEW, dengan pin producer, cancellation, dan recovery. K01 belum selesai: nilai scope produksi, review/resume remote, keputusan canonical baru/merge/split, serta gold/model acceptance. Prioritas provider pengguna adalah lokal; nama/versi final belum dipin. Smoke protokol Ollama `qwen2.5:7b` berhasil pada fixture sintetis, bukan acceptance. Lihat [deskripsi implementasi](semantic-resolution.md), [verifikasi bukti kandidat](verification-report-candidate-evidence.md), [verifikasi planner](verification-report-candidate-planner.md), dan [verifikasi submit](verification-report-policy-submit.md).

## M01/N01: native embedding dan reranking

Runtime C++ ONNX, tokenizer native, model export/reference/parity, client Go/Rust, dan verifikasi proses nyata tersedia. BGE-M3 serta reranker v2 M3 terpin telah menghasilkan output nyata pada GPU lokal. Lihat [panduan integrasi](native-inference.md) dan [bukti verifikasi](verification-report-native-models.md). Status keseluruhan M01/N01 tetap terbuka untuk model-quality/Recall/nDCG gold, workload lengkap pada profil referensi, serta bagian M01 PDF/OCR/model lain. Jangan mengubah status semua paket menjadi selesai dari numerical parity. X01 memakai client ini untuk pembangunan indeks berikutnya.

## Kemajuan integrasi RAG 2026-10-03

Pembaruan 2026-10-04: paket **worker INDEX dan admission output** sudah terverifikasi.
Plan mengikat source/target snapshot, generation, dictionary/statistik dan selection;
Rust menghasilkan IndexBatch dense+sparse, Go memeriksa hasil terhadap source.
Lihat [kontrak INDEX](index-build.md) dan [bukti verifikasi](verification-report-index-build.md).
Katalog generation/point PostgreSQL dan library writer snapshot awal Qdrant kini
tersedia. Preparation mengikat plan/source/checkpoint/dictionary; intent mendahului
I/O, full point readback dan probe serving mendahului receipt. Tes backend nyata
membuktikan lost reply/retry, publication terisolasi dan blokir saat receipt Neo4j
belum ada. Lihat [kontrak writer awal](initial-index-writer.md) dan
[bukti verifikasi](verification-report-initial-index.md). X01 tetap terbuka untuk
coordinator plan/dispatch, inventory sumber otoritatif, backend wajib profil penuh,
writer Neo4j, closure/recovery dan workload produksi. Pengujian writer baru masih
memakai checkpoint, statistik dan vector fixture; bukan acceptance RAG.

Q01 kini memiliki branch dense native dan BM25, candidate workflow paralel dengan
RRF dan error tanpa fallback diam-diam. A01 memiliki generator draft terstruktur,
span UTF-8 dan citation dari metadata tepercaya; klaim tetap UNREVIEWED/PARTIAL.
`RAGWorkflow.AnswerPinnedQuestion` menghubungkan search, port hidrasi serta
context/generation dengan fixture integrasi dan reviewer independen.
Library admission snapshot, lease selama request dan hidrasi storage kini
tersambung sampai draft bersitasi dalam tes PostgreSQL/Qdrant nyata dengan
embedding/generator sintetis. Lihat [kontrak](pinned-evidence.md) dan
[bukti verifikasi](verification-report-pinned-evidence.md). Alur PDF nyata masih
menunggu wiring coordinator INDEX, API/CLI jawaban dan tokenizer prompt
generator sebenarnya. Reranker terpin opsional telah tersambung pada 2026-10-09; streaming,
graph serta acceptance juga belum selesai. Lihat
[laporan integrasi RAG](verification-report-rag-workflow.md).

Pembaruan berikutnya 2026-10-04: `query-evidence` kini mengekspos native embedding,
dense/BM25 dan hidrasi terpin sebagai perintah operator. Factory memilih physical
store dari katalog, melakukan admission baca tanpa bootstrap dan menyiapkan BM25
terverifikasi. Warm binding library diuji terpisah dari cold invocation CLI.
Lihat [panduan](query-evidence.md) dan [verifikasi](verification-report-query-evidence.md).
Coordinator INDEX untuk corpus pengguna, generator/tokenizer prompt nyata, API
terautentikasi, graph dan acceptance tetap terbuka; gold tetap tahap berikutnya.
Prioritas pengguna 2026-10-05: demo runnable sebelum interview. Baseline lokal
BM25 + model Ollama dari sampel PDF tersedia melalui CLI `demo`, dengan panduan
di [interview-demo.md](interview-demo.md). Ini bukan penutupan K01/X01/Q01/A01
produksi; pekerjaan coordinator, graph/dense dan acceptance tetap berlaku.

Pembaruan 2026-10-08: admission writer dan source hydration kini menerima alamat
content-addressed `DocumentBatch`/`IndexBatch` Rust yang berbeda dari logical record
ID, sambil mempertahankan exact identity plan/lexical serta hash/corpus/checkpoint.
Uji kedua bentuk alamat mencakup publication, query/hydration dan draft dengan
PostgreSQL/Qdrant nyata; output model tetap sintetis. Boundary ini tidak lagi
menjadi penghambat identitas, tetapi coordinator INDEX corpus nyata tetap pekerjaan
berikutnya. Bukti dan batas ada pada [laporan](verification-report-artifact-identities.md).

Pembaruan Q01 2026-10-09: reranking native berbatas byte/jumlah pasangan kini
tersambung setelah hidrasi sumber pada library RAG dan CLI query-evidence.
Kegagalan/truncation tidak turun diam-diam ke fusion; model/skor dan accounting
tersedia pada output. Integrasi PostgreSQL/Qdrant memakai model sintetis;
kualitas, latency required dan Hybrid GraphRAG penuh belum dinyatakan lulus.
Lihat [verifikasi reranking](verification-report-evidence-reranking.md).

Pembaruan X01 2026-10-09: population Rust kini menghitung vocabulary/DF dari
rendered chunk terverifikasi; CLI vocabulary/freeze dan allocator Go
prepare-dictionary diuji bersama PostgreSQL/FileStore nyata. Statistik memakai
input policy INDEX yang sama. Penyimpanan inventory durable, registration
dependencies statistik dan scheduler INDEX masih diperlukan untuk corpus otomatis.
Lihat [panduan populasi](lexical-population.md) dan
[verifikasi](verification-report-lexical-population.md).

Pembaruan X01 2026-10-09 berikutnya: inventory plan/child job kini disimpan atomik,
direplay identik dan dipulihkan melalui admission sumber ulang. Claim INDEX
memeriksa publication aktif, retry budget dan fence; generic claim menghindari
child berinventory. Executor/output commit durable serta daemon/publication
masih terbuka. Lihat [kontrak](index-job-inventory.md) dan
[verifikasi](verification-report-index-jobs.md).

Lanjutan boundary INDEX: commit output terverifikasi kini menyimpan checkpoint
dan STAGED atomik dengan pemeriksaan ulang source, publication fence dan lease
setelah lock. Regression PostgreSQL termasuk batch128x1024 lulus; wiring executor,
daemon dan pengumpulan semua output menuju publication tetap pekerjaan berikutnya.

Pembaruan executor X01: processor memakai preflight authority per job, cache satu
inventory admitted, worker RPC dan commit STAGED; workflow membatasi deadline,
cancellation serta retry, dan daemon menambahkan kelompok INDEX opt-in. Lost
commit acknowledgement dikonfirmasi terhadap checkpoint/STAGED byte-identik.
Tes PostgreSQL memakai worker sintetis dan belum merupakan run corpus nyata.
CLI persiapan inventory dan pengumpulan/publication lengkap tetap terbuka.
Lihat [verifikasi executor](verification-report-index-executor.md).

Pembaruan X01 2026-10-09: CHUNK daemon tanpa snapshot dapat diikat melalui envelope dan receipt immutable; recovery INDEX lama memakai ulang output terverifikasi tanpa RPC. Aggregation lengkap tersambung ke publication dense/BM25 dan CLI `publish-index`, dengan late cancellation diperiksa sebelum activation. Fixture PostgreSQL/Qdrant dan review independen PASS; native/corpus penuh serta benchmark belum diukur. Persiapan snapshot/population/inventory lewat CLI dari data nyata tetap langkah berikutnya. Lihat [kontrak](index-source-publication.md) dan [verifikasi](verification-report-source-publication.md).
