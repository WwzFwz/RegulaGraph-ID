# Verifikasi bootstrap database dan konteks model EXTRACT

Dokumen ini mencatat dua paket yang dikerjakan bersamaan: CLI migrasi PostgreSQL
dan pin vocabulary ontology untuk ekstraksi. Ia memisahkan kebenaran mekanisme
yang diuji dari kegagalan ekstraksi model nyata yang masih harus diperbaiki.

## Scope yang lulus

`migrate` memakai runner yang sudah ada, DSN environment, direktori SQL tepercaya,
timeout dan JSON status. Tes PostgreSQL schema disposable membuktikan apply/replay
tanpa duplikasi, checksum drift ditolak, SQL gagal di-rollback per file, serta
seluruh migration repositori dapat diterapkan lalu diulang. Error CLI tidak
mencetak DSN/credential/SQL. Cleanup advisory lock kini memakai context terpisah
5 detik dan menutup koneksi bila unlock gagal; jaringan gagal saat unlock belum
disimulasikan.

EXTRACT mengirim ontology tervalidasi, termasuk endpoint types, qualifier kinds,
origins dan self-edge rules, sebagai system context tersendiri. Base prompt dan
teks sumber tidak ditulis ulang. Hash exact rendering masuk producer; coordinator
memerlukan hash yang sama pada persisted request sebelum dispatch dan pada output
sebelum commit, termasuk recovery. Missing/drift ditolak. Model/schema/config
di-clone saat service dibangun, dan context dihitung dalam admission byte counting.

Reviewer awal menemukan pin hanya tercatat tanpa pemeriksaan coordinator,
konfigurasi caller masih mutable, dan CountPrompt belum menghitung context baru.
Ketiganya diperbaiki dan diuji ulang independen. Pin menunjukkan konfigurasi yang
diharapkan, bukan attestation bahwa provider tidak mengganti bobot di luar kontrol.
Job EXTRACT lama tanpa context pin harus memakai producer baru dan submit baru;
artefak historis tidak ditulis ulang untuk menambahkan hash.

## Bukti pemeriksaan

Base revision `0c04770b04530bc194b23dfccdb4920951966f73` ditambah source fingerprints
pada `artifacts/verification/20261009-real-extract/independent-results.json` serta
`independent-migration-results.json`. Go 1.26.8 windows/amd64. Semua log berikut
berada pada root run tersebut.

| Pemeriksaan | Hasil dan log |
| --- | --- |
| `go test ./src/server/...` | Exit 0, `go-final.log`; integrasi opt-in tanpa environment tetap SKIP |
| `go vet ./src/server/...` | Exit 0, `go-vet.log` |
| Ontology, semantic, coordinator, tokenizer regression | PASS; `regressions.log`, `independent-units-final.log` |
| CLI actual PostgreSQL apply/replay/drift/rollback + repository migrations | PASS; `migrate.log`, `independent-migration.log` |
| Re-review cleanup migration | PASS_SCOPED; `independent-migration-cleanup.log`; no injected disconnect claim |

## EXTRACT nyata belum lulus

PDF PP No.12 Tahun 2006 yang dipakai pada [run dokumen](verification-report-real-pdf.md)
kembali berhasil hingga 35 CHUNK. Model lokal `regulagraph-demo:latest`, berbasis
Qwen2.5 7B Q4_K_M, dijalankan pada Ollama dengan GGUF aktual di-hash sebelum run.
Model manifest/container/tokenizer/prompt/ontology pins dan PID tercatat dalam
`runtime.json` serta `v2/runtime.json`; tidak ada provider cloud/API key.

Baseline `pipeline.log` gagal EXTRACT dengan DeadlineExceeded setelah attempt
empat menit. Run `v2/pipeline.log`, setelah vocabulary context disertakan, juga
gagal deadline. Ini bukan benchmark release, bukan PASS EXTRACT, dan tidak
menyatakan semua 35 item selesai. Model/prompt/gold release belum dipilih.

Observer loopback menyimpan request/response/durasi di `observations/` untuk
diagnosis. `observed-model-diagnostics.json` memperlihatkan keluaran terpotong pada
768 completion tokens (`finish_reason: length`), offset byte mention yang tidak
cocok dengan quote, serta assertion yang merujuk local ID belum dideklarasikan.
Parameter 768 berasal dari profil demo Ollama; profil tersebut belum cocok untuk
ekstraksi graph rinci. Sebagian respons berdurasi puluhan detik. Tidak ada
perbaikan diam-diam terhadap span, penghilangan kasus sulit, atau pelonggaran gate.

Pekerjaan berikut: profil/output budget EXTRACT eksplisit, representasi bukti
yang mengurangi beban hitung offset oleh model sambil mempertahankan exact source
validation, serta batch/checkpoint yang dapat melanjutkan item tanpa mengulang
inference berhasil. Semua perubahan membutuhkan regresi, review dan pengukuran;
gold/legal accuracy dan target `configs/benchmark-targets.yaml` tetap
REQUIRED_UNMEASURED. Deployment tidak dikerjakan.
