# Rencana implementasi Q01: retrieval hybrid pada snapshot konsisten

Dokumen ini menetapkan komponen, fungsi usulan, dan validasi retrieval berdasarkan
revision `17bba83`. Perannya menghubungkan indeks [X01](x01-implementation-plan.md)
dan graph [K01](k01-implementation-plan.md) menjadi EvidenceBundle untuk
[A01](a01-implementation-plan.md). Ini rencana implementasi, bukan laporan fitur aktif.
Kontrak mengikuti [system-contracts.md](system-contracts.md), kebijakan snapshot
[storage-consistency.md](storage-consistency.md), dan [verifikasi](verification.md).

## Tujuan dan kode yang dipakai ulang

Input: pertanyaan, principal/izin corpus, temporal scope, profil retrieval, deadline,
dan snapshot yang diminta. Output: RetrievalPlan, EvidenceBundle dengan provenance
dan completeness, durasi tiap stage, serta manifest konfigurasi/model/generation.
Snapshot dipin sekali sepanjang request; tanggal berlaku hukum tidak disamakan
dengan waktu observasi atau nomor urut snapshot.

`FuseRRF` sudah menggabungkan ranking secara deterministik dengan dedup evidence key
dan provenance. `CorrelateRerankBatch` sudah memeriksa korelasi pasangan/model/output.
Adapter native embedding/reranking tersedia. Dense, lexical, graph traversal, query
planning, dan orkestrasi penuh masih harus disambungkan; jangan menulis ulang kedua
fungsi aktif tersebut hanya karena workflow belum ada.

## 1. Kontrak query dan bukti

- Bedakan input tidak valid, izin ditolak, hasil kosong sah, branch unavailable,
  timeout, dan budget exhausted. Branch gagal tidak boleh berubah menjadi daftar
  kosong yang mengklaim pencarian lengkap.
- Profil Vector/Hybrid/Graph/Hybrid GraphRAG menyatakan branch, model, budget dan
  capability yang dibutuhkan. Fallback hanya lewat kebijakan eksplisit dan tercatat
  sebagai effective profile; tidak mengaku Hybrid GraphRAG ketika graph tidak dipakai.
- Temporal scope mempertahankan as-of/compare/unknown. Pertanyaan tanggal ambigu
  dapat memerlukan klarifikasi. Audit constraint tanggal pada Answer agar jalur
  klarifikasi tidak dipaksa mengarang tanggal untuk melewati validator.
- Definisikan semantik required evidence/path sets secara formal sebelum packing:
  anggota satu set yang wajib bersama, alternatif set yang sama-sama memadai, dan
  dependency setiap path. Jangan menyamakan alternatif pembuktian dengan kewajiban
  memakai seluruh alternatif. Jika wire belum cukup, review C01 lintas runtime.
- Provenance menyimpan rank/score asal, keputusan filter, source/version, path support,
  model/generation, dan truncation. Summary/profile hanyalah petunjuk mencari bukti;
  skor tinggi tidak mengubahnya menjadi kutipan pasal.
- Entity linking membaca registry revision/alias/assignment view yang diikat immutable
  ke publication snapshot K01. Jangan memakai lookup latest registry untuk query
  snapshot historis atau melihat merge/alias yang belum dipublikasikan.
- Tetapkan owner read lease: standalone `SearchEvidence` memperoleh dan melepas pin
  sendiri; saat dipanggil A01, ia menerima pin milik workflow jawaban dan tidak
  melepasnya. `PinActiveSnapshot` saja belum menerima requested historical snapshot;
  tambah admission yang memeriksa published/retained/authorized snapshot dan read lease.
  Pin perlu renewal/deadline dan pelepasan pada success/error/cancel; eviction/GC
  tidak boleh menghapus artefak yang masih dibutuhkan pembaca aktif.

## 2. Komponen dan fungsi yang direncanakan

Semua path berikut relatif terhadap `src/server/internal/`.

| Lokasi | Fungsi usulan | Input → output dan batas tanggung jawab |
| --- | --- | --- |
| `retrieval/query/normalizer.go` | `NormalizeQuestion` | Teks asli → bentuk pencarian + pemetaan perubahan; nomor hukum, negasi, dan bentuk asli tetap tersedia. |
| `retrieval/query/classifier.go` | `PlanRetrieval` | Intent/temporal/profile/capabilities → rencana branch dan budget. Classifier tidak diberi hak diam-diam membuang jalur bukti. |
| `retrieval/query/entity_linker.go` | `LinkQueryEntities` | Mention → kandidat canonical bersumber pada snapshot; read-only dan menyimpan ambiguitas. |
| `retrieval/filters.go` | `BuildRetrievalScope`, `ValidateVisibleEvidence` | Principal + snapshot + temporal → filter backend dan pemeriksaan final yang konsisten. |
| `retrieval/dense.go` | `RetrieveDense` | Query embedding purpose QUERY yang kompatibel → kandidat Qdrant terfilter dengan pin generation. |
| `retrieval/lexical.go` | `RetrieveLexical` | Query dengan analyzer/dictionary/statistics X01 → sparse BM25; tidak mengubah dictionary saat request. |
| `retrieval/graph/traversal.go` | `TraverseEvidenceGraph` | Entity/seeds + scope + budget → path berbukti dan frontier yang belum dieksplorasi. |
| `retrieval/graph/evidence.go` | `ResolvePathEvidence` | Assertion/path → sumber, pasal dan kondisi pendukung dengan identitas versi. |
| `retrieval/reranking.go` | `BuildRerankPairs`, pemanggilan `CorrelateRerankBatch` | Kandidat + teks terverifikasi → batch native dan ranking yang menjaga required support groups. |
| `retrieval/hydration.go` | `HydrateEvidenceBundle` | ID hasil ranking → chunk/span/parent/source/path authoritative dalam pembacaan batch. |
| `workflows/retrieval.go` | `SearchEvidence` | Menjalankan seluruh DAG, cancellation, pin dan telemetry; dipakai API, CLI, answering dan evaluator. |
| Adapter PostgreSQL snapshot dan workflow | `AcquireRequestedSnapshot`, `ResolveSnapshotRegistryView` | Admission snapshot aktif/historis serta registry binding terpin; gunakan scope pin yang sama sampai konsumen selesai. |
| `adapters/neo4j/` dan `adapters/qdrant/` | query backend terparameterisasi/batch | Hanya akses backend; aturan evidence, ranking dan traversal tetap di retrieval. |

## 3. Urutan runtime dan perhatian performa

1. Autentikasi/otorisasi, validasi input, admission/deadline, lalu pin snapshot dan
   representation handles. Snapshot tidak berubah meskipun publication baru terjadi.
2. Normalisasi dan rencana query. Mulai dense dan BM25 secara paralel setelah input
   masing-masing siap. Entity linking dapat berjalan bersamaan jika independen.
3. Graph memakai seed dari linking dan/atau kandidat tahap awal. Ini dependency dua
   gelombang, bukan asumsi tiga branch selalu dapat mulai bersamaan. Seed batch dibatasi
   dan provenance sumber seed tetap dicatat.
4. Filter kandidat, gabungkan via RRF, hidrasi teks untuk reranking secara batch,
   panggil cross-encoder, lalu hidrasi/validasi bukti final. Record internal dapat
   di-cache dalam request agar teks tidak dibaca ulang pada setiap stage.
5. Lengkapi dependency/path yang diperlukan dan keluarkan bundle. Refill setelah
   post-filter harus berbatas, terukur, dan melaporkan partial ketika pencarian berhenti.

Traversal memeriksa visibility setiap node, assertion dan support pada snapshot yang
sama; arah, qualifier, pengecualian dan interval menjadi bagian bukti. Gunakan frontier
batch, deteksi siklus, pagination deterministik dan cancellation; jangan memuat seluruh
graph ke RAM atau melakukan RPC per edge. Selesai karena budget berbeda dari selesai
menelusuri ruang pencarian. Hop/pruning hanya diaktifkan sebagai kebijakan terukur,
bukan klaim otomatis meningkatkan kualitas atau bukti bahwa tidak ada jawaban.

Reranking tidak boleh menghilangkan penghubung yang membuat jalur multi-hop sah.
Pertahankan support group bersama atau nyatakan set tidak lengkap. Token truncation
pada pasangan dan pemilihan kandidat tercatat agar evaluasi dapat membedakan kesalahan
retrieval dari reranker. Penambahan bukti setelah reranking menyimpan alasan/rank asal;
jangan memberinya skor reranker rekaan.

Cache lintas request, bila diaktifkan, mengikat izin corpus, snapshot, temporal scope,
generation, model/prompt/config dan teks query. Jangan mengaktifkan result/query-embedding
cache pada profil benchmark yang melarangnya. Ukur queue time, critical path wall time,
p50/p95/p99, kandidat/hop/token, memory dan error; latency paralel tidak dihitung dengan
menjumlahkan seluruh durasi branch.

## 4. Matriks validasi

| Area | Pemeriksaan dan expected behavior |
| --- | --- |
| Query | Typo, slang, code-switch, Pasal 1 vs 11, negasi dan tahun: bentuk asli terjaga; normalisasi tidak mengubah identitas hukum. |
| Isolasi/temporal | Lintas corpus, update saat query, tanggal batas, unknown/repealed dan compare: tidak ada campuran snapshot atau versi yang diasumsikan berlaku. |
| Pin/registry | Snapshot diminta belum published/retained, alias/merge belum published, registry lebih baru, renewal gagal dan GC saat A01 bekerja: reject/cancel aman atau pertahankan view lama; Q01 tidak melepas pin milik A01. |
| Representation | Model/analyzer/dictionary salah, generation belum siap dan sparse OOV: reject atau hasil sah yang eksplisit; tidak memilih generation terbaru diam-diam. |
| Branch | Empty sah, failure, timeout, cancel dan budget habis: status berbeda, deadline menyebar, tidak ada goroutine/backend request tertinggal. |
| Graph | Siklus, fan-out besar, reverse predicate, edge support tidak visible, qualifier/exception, alternatif paths: path tetap dapat dibuktikan, batas eksplorasi dilaporkan. |
| Fusion/rerank | Ties, duplicate version, cross-branch supports, pasangan reorder/hilang, truncation: determinisme dan seluruh provenance terjaga. |
| Hidrasi | Chunk corrupt, source dihapus dari snapshot, span salah, parent hilang: bukti tidak diterima sebagai lengkap; batch read tidak menyebabkan N+1 request. |
| Integrasi nyata | PostgreSQL/Qdrant/Neo4j + native embedding/reranker: keempat profil lewat workflow yang sama, artefak manifest dan stage timings dapat dievaluasi E01. |
| Incremental | Query pinned lama ketika snapshot baru terbit: jawaban pencarian lama stabil; query baru menggunakan representasi dan visibility yang kompatibel. |

## 5. Paket pengerjaan dan kriteria selesai

Urutan commit: (1) query/scope/branch contracts, (2) dense dan lexical dengan X01,
(3) graph traversal/evidence dengan K01, (4) fusion/rerank/hydration integration,
(5) workflow/API/CLI evidence search, (6) fault/concurrency/performance runs dan docs.
Pekerjaan query/library dapat maju sebelum semua backend tersedia, tetapi test mock
tidak menggantikan integrasi backend nyata. Reviewer independen memeriksa isolasi
snapshot, legal versioning, required paths, dan boundary Q01–A01 pada paket koheren.

Target tetap [benchmark-targets.yaml](../configs/benchmark-targets.yaml), mengikuti
[kebijakan benchmark](benchmark-policy.md). Sebelum gold lengkap, validasi algoritma,
invariant, fault handling dan latency yang prasyarat workload-nya terpenuhi dapat
dijalankan. Recall@k, nDCG@k, all-required-evidence/path coverage dan perbandingan profil
belum dinyatakan lulus tanpa G01 sah. Fixture dan smoke dokumen nyata hanya membuktikan
perilaku yang benar-benar diperiksa. Catat kualitas NOT_MEASURED/BLOCKED secara terpisah
dari kesiapan kode; setelah G01 lakukan tuning dev dan acceptance held-out test B01.
