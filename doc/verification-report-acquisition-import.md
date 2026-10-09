# Verifikasi impor acquisition

Dokumen ini mencatat boundary collector record/PDF ke storage bersama dan job
PARSE durable. Baseline `19e258cb60433fe6d0dfba3534c70e468a8af63e`; fingerprint
implementasi, command/exit code dan log independen berada di
`artifacts/verification/20261009-acquisition-import/independent-results.json`.
[Kontrak](acquisition-import.md) menjelaskan penggunaan serta recovery.

| Pemeriksaan | Expected dan actual | Status |
| --- | --- | --- |
| Provenance | Sources dedup per hash; mirror observations dan metadata dipertahankan; template asli tidak dimutasi | PASS |
| Admission | Sources yang sudah diisi, oversized receipt/metadata/PDF dan missing required receipt ditolak | PASS |
| Copy failure | PDF kedua hilang tidak memicu registration; perbaikan sumber dapat direplay | PASS |
| Corruption/cancellation | Source corrupt ditolak walau destination valid; cancellation tidak mendaftarkan artefak | PASS |
| PostgreSQL | Dua corpus mengimpor PDF identik dengan ID/key terpisah; replay mempertahankan job dan refs | PASS |
| Command | Record unknown/trailing JSON ditolak; corrupt root tidak menghasilkan job; observations durable identik | PASS |
| PDF aktual | PP No. 12 Tahun 2006, 14.757 bytes, masuk dua corpus dalam schema terisolasi hingga QUEUED PARSE | PASS |
| Go | Full suite dan vet exit 0; tes backend opt-in dijalankan terpisah | PASS |
| Review | Agent independen membaca diff dan mengulang focused unit/PostgreSQL/PDF aktual | PASS_SCOPED |
| Parsing/model/graph aktual | Tidak dijalankan oleh paket ini | NOT_MEASURED |
| Required performance | Tidak ada workload acceptance eligible | NOT_MEASURED |

PDF aktual berhash
`c2c761194c0a15d1858c38c7c45308e045539a80be5fd67b23cd4a050f7dbbfb`,
dari record `423b6a542356f377cf9c0bfe1f3382c91700e2464ab82a06946add0204bf91c9.json`.
File asli tidak diubah; schema/database dan destination tes terisolasi.
Raw logs: unit.log, postgres.log, real-record.log, go-suite.log, vet.log,
independent-final.log dan independent-real-record.log pada folder run tersebut.

Review menemukan konflik storage_key global ketika PDF dipakai dua corpus,
serta metadata besar yang dapat digandakan sebelum wire validation. Perbaikan
memisahkan refs read/tujuan corpus-scoped dan menambah admission sebelum ekspansi.
Regression serta review ulang lulus. Tidak ada perubahan wire schema atau target
benchmark. Import tidak memvalidasi canonical identity, isi hukum atau seluruh
pipeline; queued bukan completed/published.
