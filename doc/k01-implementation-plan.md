# Rencana implementasi K01: resolusi dan graph berbukti

Dokumen ini merencanakan penyelesaian knowledge graph setelah fondasi pada revision
`17bba83`. Nama fungsi/file baru adalah usulan implementasi, bukan klaim fitur aktif.
Perannya menghubungkan resolusi identitas, assembly Rust, dan publication Go dengan
[kontrak sistem](system-contracts.md), [storage](storage-consistency.md), serta
[verifikasi pipeline](verification-pipeline.md). Baca bersama rencana
[X01](x01-implementation-plan.md), [Q01](q01-implementation-plan.md), dan
[A01](a01-implementation-plan.md).

## Hasil yang dituju dan fondasi yang dipakai

Input adalah EXTRACT/CHUNK terverifikasi, candidate policy, registry revision, dan
snapshot sumber. Output adalah keputusan resolusi teraudit, GraphDelta immutable,
dependency manifest, serta graph yang dapat dibaca pada snapshot terpublikasi.
Semua mention tetap terhitung: resolved, deferred, atau rejected dengan alasan.
Model membantu menilai ambiguitas; model tidak langsung mengubah canonical registry.

Gunakan kembali candidate planner, `Semantic.ResolveBatch`, hidrasi bukti kedua sisi
LINK, audit/replay cache, `SemanticExecutor`, penyimpanan proposal WAITING_REVIEW,
`LoadPendingSemanticProposal`, dan builder ResolutionBatch. Detail status aktif
berada pada [semantic-resolution.md](semantic-resolution.md). Builder ResolutionBatch
tersebut bukan implementasi assembly GraphDelta; assembly dan adapter Neo4j masih
pekerjaan berikutnya. Alias exact yang tersedia belum menjamin recall kandidat semantik.

## 1. Audit kontrak dan kebijakan sebelum perubahan registry

- Bedakan proposal, keputusan review, commit registry, dan checkpoint job. Keputusan
  memakai expected revision, hash proposal, policy version, principal terautentikasi,
  alasan, serta bukti sumber. Field actor dari body request bukan bukti otorisasi.
- Audit representasi create canonical, LINK, DEFER, MERGE, dan SPLIT pada schema
  yang ada. Jika belum cukup, perluas C01 dengan validasi seluruh runtime dan review
  kompatibilitas; jangan membuat JSON kontrak publik tandingan.
- Canonical baru memerlukan jenis, namespace/scope, dan identitas yang dibuktikan.
  Dua nomor regulasi sama dari penerbit berbeda tidak otomatis satu entitas. Alias
  sinonim dapat menunjuk satu canonical hanya jika referennya memang sama.
- MERGE/SPLIT harus menjadi perubahan berversi dengan redirect/mapping yang dapat
  diaudit dan dependency fan-out untuk U01. Jangan menulis ulang ID pada snapshot lama
  atau menghilangkan provenance ketika satu sumber menarik dukungannya.
- Publication wajib memiliki binding immutable dan authoritative dari corpus snapshot
  ke registry revision serta view alias/assignment yang digunakan graph. Audit lokasi
  binding pada schema/storage: `SnapshotRef`/`PublicationManifest` saat ini belum
  langsung membawa registry revision. `LookupCanonicalAliases` yang membaca revision
  terbaru corpus tidak boleh dipakai sebagai lookup historis Q01. Sediakan lookup
  registry pada revision terpin dan pertahankan history yang diperlukan pembaca.
  Perubahan registry yang belum dipublikasikan tidak terlihat oleh query snapshot lama.
- Scope dan tanggal berlaku yang unknown tetap unknown. Bedakan explicit/inferred,
  confidence model, dan keputusan yang sudah disetujui. Skor model bukan probabilitas
  terkalibrasi atau dasar otomatis melewati kewajiban review.
- Jalur default tetap review sebelum perubahan identitas. Aktivasi auto-approval
  memerlukan policy konkret yang disetujui pengguna dan dievaluasi; implementasi
  review/resume tidak perlu menunggu keputusan auto-approval.

## 2. Komponen dan fungsi yang direncanakan

Path pada tabel relatif terhadap runtime pemilik; nama tambahan tidak mengganti fungsi
aktif yang sudah memenuhi kontraknya.

| Komponen/lokasi | Fungsi usulan | Input dan output/peran |
| --- | --- | --- |
| Go `internal/workflows/semantic_review.go` | `ReviewSemanticProposal`, `ResumeResolvedJob` | Principal + proposal hash + expected revision menjadi keputusan durable dan resume yang idempotent. |
| Go `internal/adapters/postgres/registry_review.go` | `CommitReviewedResolution`, `LoadReviewOutcome` | Commit keputusan/registry/checkpoint secara atomik bila satu transaksi memungkinkan; jika terpisah gunakan intent dan replay durable. |
| Go adapter PostgreSQL registry/publication | `BindSnapshotRegistryView`, `LookupCanonicalAliasesAtRevision` | Binding immutable snapshot → revision; alias/assignment historis dari binding terverifikasi, bukan latest mutable state. |
| Go `internal/domain/` dan adapter registry terkait | `PlanCanonicalChange`, `ValidateCanonicalChange` | Rencana create/merge/split berversi dengan invariant scope, provenance, dan invalidation; mutasi hanya melalui transaksi Go. |
| Go planner kandidat yang sudah ada | `ExpandSemanticCandidates`, `MergeCandidateEvidence` | Tambahkan alias bersumber atau kandidat lexical/dense yang terikat scope/type; dedup kandidat sambil menjaga seluruh dukungan sumber. |
| Rust `knowledge_graph/assembly/builder.rs` | `assemble_graph_delta` | EXTRACT + resolution receipt + document batch menjadi node/assertion/support/closure dan dependency manifest deterministik. |
| Rust `knowledge_graph/validation/checks.rs` | `validate_graph_delta` | Periksa ontology, endpoint, scope, source spans, temporal, revision, dan seluruh item terhitung sebelum mengeluarkan batch. |
| Rust `knowledge_graph/summarization/profiles.rs` | `build_entity_profiles` | Profil bersumber dengan daftar dependency; tidak menjadi pengganti pasal asli atau fakta otoritatif. |
| Go `internal/adapters/neo4j/` | `EnsureGraphSchema`, `ApplyGraphDelta`, `VerifyGraphReady`, `ReadVisibleAssertions` | Constraint/index, mutasi batch terparameterisasi, verifikasi hasil, dan pembacaan visibility pada snapshot. |
| Go `internal/workflows/graph.go` | `GraphExecutor.RunOnce`, `StageGraphPublication` | Dispatch ASSEMBLE, validasi artefak, intent/receipt backend, recovery, dan publication bersama indeks yang diwajibkan profil. |
| Go API/CLI review | handler review dan inspeksi proposal | Lapisan tipis menuju workflow; autentikasi, otorisasi corpus/action, expected revision dan audit wajib. |

Penempatan coordinator final berada pada workflow Go; adapter Neo4j hanya memiliki
akses backend. Rust menghasilkan GraphDelta dan tidak menghubungi registry/Neo4j.
Candidate expansion dapat memanfaatkan X01 setelah indeks tersedia; exact candidate
planner tetap jalur dasar. Jangan memperkenalkan dependency melingkar bahwa assembly
wajib memakai indeks yang hanya boleh dibuat setelah assembly: gunakan indeks chunk
yang capability-nya dinyatakan, atau kandidat dari snapshot sebelumnya dengan proof
membership yang valid. Ketiadaan indeks berarti kemampuan expansion belum tersedia.

## 3. Aturan assembly dan publication

Identitas assertion mencakup subject/predicate/object, arah, qualifier, kondisi,
pengecualian, interval yang diketahui, serta explicit/inferred. Dukungan sumber disimpan
terpisah. Dua sumber mendukung assertion sama tidak memaksa dua fakta; mencabut satu
sumber tidak menghapus dukungan lain. Mirror dokumen juga tidak dihitung sebagai dua
dukungan independen untuk menaikkan confidence.

Hubungkan pasal, ayat, definisi, rujukan, perubahan/pencabutan, dan entitas menggunakan
ID yang sudah dibuktikan. Mention deferred tidak menjadi endpoint canonical rekaan;
laporkan dampaknya terhadap completeness. Closure harus mengikat versi/visibility dan
dependency terkait, bukan menghapus global node yang masih dipakai sumber lain.

Go menyimpan intent dan before-image sebelum mutasi backend, menerapkan fence/replay,
memverifikasi counts, checksum, endpoint, dukungan dan visibility, lalu mengeluarkan
receipt. Publication memakai coordinator S01 dan CAS snapshot. Jika profil mensyaratkan
Neo4j dan Qdrant, keduanya harus memenuhi readiness; graph saja bukan publication lengkap.
Ordering/read consistency driver dipilih dan diuji pada versi Neo4j yang dipin. Proof
readiness harus berlaku untuk semua route/replica yang boleh melayani pembaca snapshot.
Lease PostgreSQL sendiri tidak menghentikan write backend lama yang masih berlangsung.

Profil entitas dibuat setelah bukti dan identitas stabil, dengan model/prompt terpin.
Profil boleh di-cache berdasarkan dependency fingerprint; perubahan alias/assertion
terkait menginvalidasinya. Jika pembuatan profil gagal, kebijakan kemampuan snapshot
harus eksplisit; jangan menyembunyikan profil hilang sebagai graph lengkap.

## 4. Validasi input, proses, output

| Area | Kasus dan hasil yang harus dibuktikan |
| --- | --- |
| Review/resume | Actor spoofing, lintas corpus, proposal berubah, stale revision, double-submit, lease habis, cancel: tidak ada commit tanpa otorisasi; retry mengembalikan keputusan yang sama. |
| Model/kandidat | Kandidat tidak ada, alias ambigu, bukti kedua sisi tidak cocok, overflow dan model invent ID: reject/defer eksplisit; ukur kandidat benar yang terlewat secara terpisah dari keputusan resolver. |
| Identitas | Nomor sama penerbit berbeda, alias satu nama dua entitas, unknown date, create duplikat dan merge/split: tidak ada false identity akibat normalisasi atau overwrite history. |
| Registry snapshot | Merge/alias baru committed tetapi belum published, lalu publikasi snapshot baru: query snapshot lama tetap memakai alias/assignment revision lama, termasuk sesudah restart. |
| Assembly | Endpoint hilang, arah terbalik, qualifier/exception berbeda, offset UTF-8 salah, unresolved mention: batch ditolak atau completeness yang diizinkan dinyatakan; tidak ada fakta diam-diam hilang. |
| Dukungan | Dua sumber satu assertion, mirror, withdrawal satu sumber, alias dipakai silang: dukungan lain dan snapshot lama tetap dapat dibaca. |
| Storage/recovery | Crash sebelum/sesudah receipt/CAS, stale writer, closure parsial dan backend terlambat: replay aman, snapshot parsial tidak terpublikasi, committed snapshot tidak dibatalkan. |
| Runtime nyata | Dokumen nyata → model lokal terpin → review terotorisasi → GraphDelta → PostgreSQL/Neo4j aktual → query snapshot; fixture deterministik tetap digunakan untuk fault injection. |
| Profil/U01 | Ubah satu fakta/alias: hanya profil dan artefak dependen yang invalid; output incremental sama dengan rebuild pada input dan konfigurasi yang sama. |

## 5. Performa, urutan paket, dan selesai

Batch kandidat, hidrasi, registry, serta graph writes; hindari RPC per mention/edge.
Batasi memory/frontier/artifact bytes dan ukur biaya kandidat, jumlah assertion/support,
throughput, queue time, latency p50/p95/p99, peak RSS, serta retry/write amplification.
Cache terikat model/prompt/policy/source/snapshot; pekerjaan bulk tidak menghabiskan
kapasitas query. Batas kandidat harus terlihat pada output dan diuji dampak recall-nya.

Urutan paket commit: (1) review/resume dan perubahan canonical, (2) candidate expansion,
(3) Rust assembly/validasi, (4) Neo4j writer/readiness/recovery, (5) worker dan publication,
(6) profil/invalidation serta dokumentasi milestone. Jika expansion memerlukan indeks,
kerjakan assembly dari keputusan sah yang tersedia terlebih dahulu.

Reviewer independen memeriksa perubahan schema/identity, storage/publication, lalu
integrasi final berdasarkan diff dan bukti aktual. Simpan raw log/fingerprint mengikuti
[protokol verifikasi](verification.md). Seluruh angka tetap di
[benchmark-targets.yaml](../configs/benchmark-targets.yaml). Full gold G01 dikerjakan
setelah pipeline kode tersambung; precision/recall/F1 resolution/extraction dan false
merge/split tetap NOT_MEASURED sampai data valid tersedia. Kode terintegrasi dan
acceptance kualitas adalah status terpisah; tidak menandai keseluruhan K01 lulus dari
tes schema, mock, atau smoke model.
