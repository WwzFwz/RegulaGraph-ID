# Laporan verifikasi K01: closure hasil RESOLVE

Dokumen ini merekam verifikasi boundary domain Go yang menolak `ResolutionBatch` tanpa ikatan jelas ke `ExtractionBatch`, bukti sumber, dan keputusan registry yang dinyatakan. Cakupan ini hanya validasi struktur hasil; stage RESOLVE, lookup registry otoritatif, dan kualitas identitas hukum belum aktif.

## Identitas dan hasil

Perubahan perilaku ada pada commit `5503881`, diuji 2026-09-23 dengan Go 1.26.8 windows/amd64. Fixture sintetis mencakup satu mention beserta sumber/byte span, satu proposal LINK, dan satu keputusan; mutasi menguji drift snapshot, auth scope, konfigurasi, hash sumber, revisi, ID record, locator, bukti, keputusan, serta item yang hilang. Raw output berada di `artifacts/verification/k01-resolution-closure-20260923/` (diabaikan Git).

| Pemeriksaan | Expected dan aktual | Status |
| --- | --- | --- |
| `go test ./...` dari `src/server` | Semua paket Go lulus; exit 0; `go-test.log` | PASS |
| `go vet ./...` dari `src/server` | Tidak ada temuan; exit 0; `go-vet.log` | PASS |
| `git diff --check` | Tidak ada kesalahan whitespace; exit 0; Git hanya memperingatkan konversi line ending pada checkout Windows | PASS |
| Review agent independen | Menemukan drift context, locator palsu, issue evidence ref palsu, overlap bukti satu byte, dan collision record ID; semuanya diperbaiki, tes adversarial ditambahkan, lalu reviewer memeriksa ulang tanpa temuan blocking terbuka | PASS untuk boundary yang ditinjau |
| Registry receipt, negative lookup, PostgreSQL end-to-end | Belum ada stage RESOLVE atau kandidat registry yang otoritatif; validator tidak membuktikan keputusan benar-benar dicatat registry | BLOCKED |
| Kualitas identitas dan benchmark required | Belum ada gold resolution set, provider/model terpilih, atau workload referensi | NOT_MEASURED |

`ValidateResolutionBatchClosure` mengharuskan seluruh mention EXTRACT tercakup oleh proposal atau error issue eksplisit; proposal memuat source/version dan rentang bukti yang mencakup mention; keputusan LINK menunjuk kandidat yang diusulkan. Pemeriksaan bukti memakai indeks interval terurut untuk menghindari pemindaian seluruh span per proposal. Locator ditolak sampai dapat divalidasi terhadap peta halaman `DocumentBatch`. Ini tidak mengautentikasi baris registry, tidak mengesahkan MERGE/SPLIT, dan tidak menjadikan kemiripan nama sebagai identitas.

Sebelum wiring RESOLVE, coordinator harus menyediakan candidate snapshot dan revision lookup yang dibekukan. CREATE berbasis hasil lookup kosong wajib merekam `DependencyManifest.lookup_scope_revisions` agar kedatangan alias atau entitas baru menginvalidasi hasil lama. Keputusan registry harus diverifikasi melalui receipt/state transaksional, termasuk replay, stale revision, dan koreksi merge/split. Semua target numerik di `configs/benchmark-targets.yaml` tetap **REQUIRED_UNMEASURED**.
