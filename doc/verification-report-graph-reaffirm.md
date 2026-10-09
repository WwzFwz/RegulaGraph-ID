# Verifikasi reaffirmation lintas registry revision

Dokumen ini mencatat bukti reuse RESOLVE historis pada target registry view baru,
dengan receipt storage yang terikat dependency dan live authority. Baseline:
`a0688f4c16df0ea19ef2bfb324b7012dbb68eabe`. Raw logs, perintah/exit code, toolchain
dan fingerprint kode berada pada `artifacts/verification/20261009-graph-reaffirm/`.
Kontrak pemakaian berada di [graph-reaffirmation.md](graph-reaffirmation.md).

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Transform | Revision envelope berubah; keputusan/proposal/model/provenance asli tetap; target berbeda menghasilkan ref berbeda | PASS |
| Kompatibilitas | Policy lama menghasilkan bytes sama; zero/rollback/same-target pada policy reaffirmation ditolak | PASS |
| Preflight beberapa sumber | Dua source dengan revision historis berbeda melewati pemeriksaan ke reservation; source unready/newer menggagalkan semua | PASS |
| Retained target | Future target ditolak untuk RESOLVE kosong dan nonempty | PASS |
| Context registry | Perubahan positive/negative scope dan unselected canonical profile ditolak; perubahan global tak terkait dapat direuse | PASS |
| Native | Registry allocation nyata setelah RESOLVE, executable preparation, Rust ASSEMBLE, PostgreSQL checkpoint, Neo4j/Qdrant publication dan retrieval draft fixture | PASS |
| Frozen replay | Registry maju lagi setelah preparation; retry mempertahankan target binding dan child ID | PASS |
| Receipt concurrency | Registry maju ketika writer menunggu snapshot lock: stale stamp ditolak, fresh replay sukses | PASS |
| Receipt corruption/pool | Candidate bytes corrupt ditolak; replay pool satu koneksi selesai tanpa nested acquisition | PASS |
| Go regression | Full Go suite dan vet exit 0; targeted backend semantic/candidate regressions lulus | PASS |
| Review independen | `/root/verify_index_jobs` membaca implementation dan mengulang unit, tiga native cases serta PostgreSQL regressions | PASS_SCOPED |
| Multi-document native publication | Native run lintas revision memakai satu source; beberapa source baru preflight unit | NOT_MEASURED |
| Gold/model/required performance | Tidak ada run pada corpus/workload acceptance | NOT_MEASURED |

Native tests mencakup baseline same-revision, publication failure recovery dan
cross-revision reaffirmation. Source EXTRACT/review labels, vektor dan generation
draft tetap fixture; bukan klaim model extraction atau ketepatan hukum nyata.
Run memakai Windows/Go lokal serta worker Rust, PostgreSQL, Qdrant dan Neo4j
yang sudah tersedia; schema fixture terisolasi dan tidak ada deployment.

Review tidak menemukan blocker terbuka dalam scope. Pemeriksaan implementer
menambahkan gate retained revision untuk mention-free RESOLVE, karena jalur ini
tidak mempunyai candidate lookup yang sebelumnya menjaga batas history. Original
resolution tidak ditulis ulang. Tidak ada perubahan Protobuf/schema lock atau
penurunan target benchmark. Uji Rust aktual membuktikan kontrak decision revision
historis di dalam derived batch view dapat dikonsumsi worker yang ada.

Reaffirmation hanya berlaku untuk context identik yang tervalidasi. Context yang
berubah tetap memerlukan replan/resolution baru; canonical merge/split, incremental
corpus lengkap dan acceptance kualitas/performa belum selesai.
