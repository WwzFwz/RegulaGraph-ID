# Verifikasi audit inventory akuisisi D01

Dokumen ini mencatat implementasi dan hasil audit inventory lokal pada 2026-09-20. Perannya mengikat
library audit, command CLI, corpus 3 GB, pengujian, dan review agent independen ke revision yang dapat
ditinjau. PASS di sini hanya berlaku untuk integrity/provenance inventory yang tersedia; coverage corpus,
kebenaran metadata hukum, canonical identity, parsing/OCR, gold labels, dan benchmark required tetap
**NOT_MEASURED** atau belum selesai.

## Revision dan artefak

Library audit berada pada revision `0327fdd` (`feat(acquisition): verify corpus artifacts and provenance`).
Wiring CLI berada pada revision `9fbe2b5` (`feat(cli): expose verified corpus inventory audit`). Raw log
lokal berada di `artifacts/verification/d01-inventory-20260920/` dan data inventory berada di
`data/acquisition/`; keduanya diabaikan Git. Target dalam `configs/benchmark-targets.yaml` tidak diubah.

Inventory ID aktual adalah `0dff5a00c9344bbc84d11b58de9c80ba0de3cbe900747ab7ab5dc3a580576b8e`.
ID mengikat hash record JSONL, seluruh observation, semantic queue set, dan blob set. HTML hash/path berada
di record JSONL sehingga perubahan source provenance mengubah inventory ID.

## Hasil corpus lokal

| Pemeriksaan | Actual | Status |
| --- | ---: | --- |
| Latest record | 616 | Terinventarisasi |
| Observation immutable | 619 | Latest wajib memiliki pasangan byte-identik |
| PDF unik | 650 | Seluruhnya hash/size/header/EOF valid |
| Byte PDF unik | 2.999.240.002 | Seluruh referenced set terbaca |
| Missing/corrupt referenced PDF | 0 | PASS |
| Orphan PDF | 0 | PASS |
| Complete / partial / failed record | 595 / 2 / 19 | 21 incomplete dipertahankan sebagai warning |
| Queue total / acquired / pending | 3.160 / 615 / 2.545 | Coverage belum selesai |
| Document reference / missing lokal | 1.690 / 526 | Follow-up discovery diperlukan |
| BPK record / complete | 516 / 513 | Parsial |
| Komdigi record / complete | 90 / 82 | Parsial |
| JDIHN record / complete | 10 / 0 | Connector/layout belum selesai |
| Format text/scan/table | `unknown_until_m01` | NOT_MEASURED |

Audit binary dengan delapan worker pada page cache hangat selesai dalam 2,330 ms. Run pertama melalui
`go run` selesai dalam 48,681 ms dan mencampur kompilasi serta cache awal. Keduanya hanya observasi
diagnostik; hardware, cold-cache setup, repeated runs, peak RSS, serta workload benchmark belum dibekukan,
sehingga tidak ada gate performa yang dinyatakan PASS.

## Pemeriksaan implementasi

```powershell
go test ./src/server/... -count=1
go vet ./src/server/...
python scripts/check_contracts.py
regulagraph audit -out data/acquisition -workers 8
```

Seluruh command correctness di atas lulus pada implementasi final. Contract check tetap menemukan 157
message, 31 enum, dan empat service tanpa menulis ulang baseline. Header `main.go` lama mempertahankan
peran, kontrak, benchmark, target numerik, dan status aktual.

## Review independen

Agent `verify_c01` menjalankan delapan counterexample library dan overlay CLI. Temuan awal menutup false
PASS untuk historical blob hilang, latest tanpa observation matching, HTML path/hash parsial, primary PDF
receipt hilang, inventory ID yang tidak berubah saat provenance berubah, junction escape, failure sebelum
HTTP body, serta historical status tidak sah. Review final library dan CLI sama-sama PASS; invalid inventory
tidak dapat menghasilkan exit 0.

Review CLI juga memeriksa argumen/help, root hilang, cancellation, stdout/write failure, JSON output, dan
exit code 0/1/2. Batas yang tersisa adalah Ctrl+C saat proses aktif, concurrent collector versus auditor,
process crash/power loss, serta benchmark cold-cache/RSS. Semua batas tersebut dicatat sebagai pekerjaan
lanjutan dan tidak disamarkan menjadi kelulusan D01 penuh.

## Kelanjutan

D01 berikutnya memperbaiki connector JDIHN, menindaklanjuti 21 record incomplete dan 526 missing reference,
serta menentukan sampling yang menjaga distribusi portal/jenis/tahun. M01 kemudian mengukur parser/OCR pada
PDF nyata dan menetapkan strata text/scan/mixed/table; hasil itu membuka I01 dan memperkaya gold G01.
