# Verifikasi generation dari PDF nyata

Dokumen ini mencatat integrasi snapshot PP 12/2006 dengan generator lokal pada
2026-10-09, optimasi metadata prompt, dan batas klaim operasional. Ini smoke
fungsional dengan satu pertanyaan, bukan acceptance kualitas atau performa.
Implementasi answering berada pada revision `fb6c02c`; CLI uji dibangun dari
working tree yang sama sebelum commit. Fingerprint binary/file dan raw logs
berada pada `artifacts/verification/20261009-real-answer/`.

## Input dan runtime

Corpus `corpus:pp12-operational-v1`, snapshot sequence 2/generation
`generation:pp12-index-v2`, berasal dari satu PDF dengan 35 chunk dan model
BGE-M3/BM25 sesuai [laporan indeks](verification-report-real-index.md).
Pertanyaan: `Kapan PP Nomor 12 Tahun 2006 mulai berlaku?`, AS_OF 2026-01-01,
profil hybrid, unresolved report. Tanggal permintaan bukan hasil legal review.

Generator Qwen GGUF Q4_K_M memakai llama.cpp `b11515-3d65c90d0`, CPU empat thread,
satu slot, context 8192, context shift nonaktif. Hash GGUF/tokenizer:
`2bada8a7450677000f678be90653b85d364de7db25eb5ea54136ada5f3933730`.
Konfigurasi compact SHA-256:
`eb1a0cac9365aadc8fed4aa5b37b361c38ff7e8a0dc7aa44349526fd21760099`.
Prompt SHA-256:
`ff376ea9a2a411ad9bfddeb79dc1fa76a6e0523bce55017687c1e18a278b5ca4`.
Context packing 2048 token, output reserve 512, maximum evidence 40. Angka ini
parameter eksperimen, bukan pengganti target `configs/benchmark-targets.yaml`.

## Riwayat gagal dan perubahan

| Percobaan | Hasil aktual |
| --- | --- |
| CLI, context 4096, prompt lama | Ditolak admission sebelum generation: full prompt + reserve melebihi window; exit 1, 4,62 detik |
| CLI, context 8192, prompt lama | Timeout 300 detik; exit 1, sekitar 301,07 detik; tidak ada jawaban yang diterima |
| API, context 8192, prompt lama | HTTP 504, sekitar 300,84 detik; model sempat menghasilkan token tetapi request tidak selesai |
| CLI, context 8192, prompt compact | Exit 0, 182,20 detik; 3395 input/108 output tokens, satu claim/citation; PARTIAL/UNREVIEWED |
| API, context 8192, prompt compact | HTTP 200, 154,78 detik; 3396 input/108 output tokens, satu claim/citation; PARTIAL/UNREVIEWED |

Kegagalan disimpan di `artifacts/operational/pp12-v1/answer/`, termasuk
`query-date-result.json`, `query-date-8192-result.json`, `api-answer-result.json`,
dan log server. Pembatalan task lama dikonfirmasi sebelum request berikutnya.
Tidak ada perubahan target benchmark atau klaim p95 dari satu sampel.

Prompt lama menghabiskan token pada identifier opaque dependency yang belum
tersedia. `prompt_dependencies.go` kini mengirim satu handle/kind per dependency,
mempertahankan urutan dan jumlah, serta menghindari collision dengan ID evidence.
Teks bukti, ID evidence/citation dan versi tidak diubah. Canonical missing refs
tetap ada dalam jawaban dan hash input audit. Full-prompt admission serta usage
parity masih wajib. Kontrak dan konsekuensi pin dijelaskan di
[jawaban lokal](local-answer.md).

## Pemeriksaan dan hasil

| Pemeriksaan | Hasil dan batas |
| --- | --- |
| `go test ./src/server/internal/answering ./src/server/internal/config ./src/server/internal/workflows ./src/server/internal/api/... ./src/server/cmd/cli ./src/server/cmd/api -count=1` | PASS, exit 0; Go 1.26.8 Windows/amd64; tes opt-in model tidak otomatis dijalankan |
| `go vet ./src/server/internal/answering ./src/server/cmd/cli ./src/server/cmd/api ./src/server/internal/api/...` | PASS, exit 0 |
| Review independen `/root/verify_index_abort` | PASS scoped; reviewer menjalankan ulang focused tests di bawah, exit 0 |
| Collision/regresi | Selected ID `missing:1` dilewati allocator; handle tidak diterima sebagai citation; canonical omission, evidence blocks dan audit mapping dipertahankan |
| CLI actual PDF + model | PASS integrasi draft dengan canonical source citation; bukan PASS kualitas hukum |
| API actual PDF + model | PASS: readiness capability `evidence_and_answer_draft`, POST `/v1/questions` HTTP 200 dengan satu claim/citation; 44 missing refs tetap dilaporkan |
| Review output independen | PASS scoped: reviewer mencocokkan corpus/snapshot, citation/version/span/page dengan evidence serta menghitung ulang canonical omission audit hash untuk CLI dan API |
| Cleanup read lease | PASS: query database setelah request terminal mengembalikan 0 row snapshot read lease |
| Gold, scale, required latency/throughput | NOT_MEASURED; tidak ada denominator gold atau profil acceptance pada run ini |

Perintah focused review (cache workspace terpisah setelah cache Windows default
menolak akses):

```powershell
go test ./src/server/internal/answering -run 'Test(MissingPrompt|DraftCompact|DraftGenerator)' -count=1
```

Expected CLI adalah jawaban yang lulus validasi referensi dari snapshot yang sama,
atau abstain/partial eksplisit. Actual `query-compact.json` berisi satu claim dan
satu citation, completion SUCCEEDED, semantic PARTIAL, support UNREVIEWED.
Output menyebut tanggal yang memang muncul pada teks Pasal II yang diambil;
locator citation berasal dari metadata PDF (halaman 5, byte 8696–9356), bukan URL
buatan model. Ini pemeriksaan kesesuaian terhadap teks fixture corpus, bukan
kesimpulan bahwa status peraturan saat ini sudah diketahui. Dependensi temporal,
konteks yang belum lengkap, dan kebutuhan semantic review tetap dilaporkan.

CLI melaporkan build `264b6d7` karena environment checkpoint lama belum diperbarui;
label itu **bukan revision binary uji**. Fingerprint binary/implementasi di manifest
run dan commit `fb6c02c` menjadi acuan. Run API compact memakai build `fb6c02c`
secara eksplisit. Raw hasil `api-answer-compact.json`, timing
`api-answer-compact-result.json`, dan readiness `api-compact-ready.json` disimpan
bersama artefak CLI di root run `answer/`. Cache model sudah hangat dari percobaan sebelumnya;
waktu tersebut bukan cold-start yang terkontrol atau perbandingan performa adil.

## Kelanjutan

Cara start/query/stop/resume berada di [handoff](operational-handoff.md). Jangan
mengulang download/INDEX untuk memakai snapshot valid ini. EXTRACT PDF nyata,
RESOLVE, graph publication, pertanyaan lain dan optimasi latency tetap terbuka.
Keberhasilan GENERATE tidak menyelesaikan cabang EXTRACT atau seluruh aplikasi.
