# Diagnostik runtime dan contoh penyalinan EXTRACT

Dokumen ini mencatat eksperimen lanjutan pada chunk PDF nyata yang sebelumnya
gagal quote alignment. Semua run gagal menghasilkan completion yang dapat diterima.
Tidak ada perubahan prompt produksi, validator, target benchmark, atau klaim
peningkatan kualitas dari eksperimen ini.

## Cakupan dan bukti

Revision kode `9a2a34c07aa3cfa683f543a18cf6e0102fb31db7`. Input berasal dari
`20261009-quote-extraction/retry/actual-request.json`: chunk pembuka PP 12/2006,
bukan seluruh PDF (35 chunk). Source text, ontology, schema v2, temperature 0 dan
cap 4096 dipertahankan. Data dan skrip diagnostik berada di
`artifacts/verification/20261009-llama-extraction/` (lokal, diabaikan Git).

Ini eksperimen engineering tanpa gold labels, bukan benchmark release atau
evaluasi kelengkapan/ketepatan hukum. Uji saved replay menggunakan fixture identity
dan offset dasar, sehingga tidak membuktikan ingestion corpus atau replay durable
hasil PDF sebenarnya. Review independen kualitas model belum dilakukan; tidak ada
model atau prompt baru yang dipromosikan.

## 1. Request v2 pada llama.cpp CPU

`python artifacts/verification/20261009-llama-extraction/run.py` memakai envelope
request sebelumnya, hanya mengganti alias model ke `regulagraph-semantic-test`.
Server lokal `b11515-3d65c90d0`, CPU 4 threads, 1 slot, context 8192, GPU layers 0,
context shift nonaktif. GGUF Qwen2.5 7B mempunyai SHA-256
`2bada8a7450677000f678be90653b85d364de7db25eb5ea54136ada5f3933730`;
file di-hash ulang setelah server dihentikan. Hash launcher/implementation,
toolchain, processor identifier dan revision disimpan pada `manifest.json`.

Preflight endpoint menghitung **2449 input tokens**; ditambah cap 4096 masih
muat di context 8192. Request menggunakan HTTP langsung untuk diagnostik, bukan
jalur Go `AdmitLlama`; hitungan ini tidak merupakan PASS seluruh admission guard.
Log menunjukkan 2048 token prompt diproses dalam sekitar 74,10 detik, lalu
generation sekitar 5,9–6 token/detik. Angka log tersebut bukan p95 atau throughput
corpus. Konfigurasi CPU ini berbeda dari Ollama, sehingga tidak merupakan
perbandingan performa yang terkontrol.

Client timeout setelah **300,016 detik**, exit 1. Tidak ada completion lengkap
untuk diperiksa. Server mencatat `cancel task` dan `stop processing` setelah
disconnect; setelah task terminal, PID 9092 dihentikan dengan pemeriksaan path
executable milik run. Tidak ada request duplikat ketika task masih berjalan.

Bukti: `actual-request.json`, `props.json`, `count.json`, `result.json`,
`server.stderr.log`, `server.stdout.log`, `manifest.json`, dan `run.py`.

## 2. Prompt eksperimen dengan contoh penyalinan pada Ollama

`python artifacts/verification/20261009-llama-extraction/run_examples.py`
menambahkan contoh sintetis newline dan konteks yang bersebelahan ke prompt v2.
Contoh dinyatakan bukan bukti dokumen. Request menggunakan model lokal
`regulagraph-demo:latest`; source/schema/ontology, temperature dan cap tetap sama.
Prompt eksperimen hanya berada di artefak; `configs/prompts/extraction-v2.md`
tidak diubah. Hash prompt eksperimen:
`e2c9e9bd00b158c56c23cdfc971a341630ebbf56b3dca13034ab96ed75140976`.

Request selesai dalam **249,719 detik**, usage 2754 prompt + 4096 completion tokens,
`finish_reason=length`. Model mengulang mention peraturan dengan local ID berbeda
sampai output terpotong. Exit 0 skrip HTTP hanya berarti respons tersimpan, bukan
ekstraksi berhasil. Tidak ada JSON proposal lengkap untuk menghitung precision,
recall atau denominator validitas seluruh locator.

Replay diagnostik:

```powershell
$env:REGULAGRAPH_TEST_QUOTE_REPLAY_DIR = (Resolve-Path 'artifacts/verification/20261009-llama-extraction/copy-examples').Path
go test ./src/server/internal/adapters/inference -run '^TestQuotedExtractionSavedModelProjection$' -count=1 -v
```

Hasil **FAIL, exit 1**, `saved diagnostic is incomplete or mismatched`.
Pemeriksaan berhenti pada completion envelope; projection/ontology/C01 belum
dijalankan pada output ini. Jangan mengklaim output lolos ataupun pemeriksaan
seluruh locator sudah selesai.

Bukti dalam `copy-examples/`: request/response mentah, `result.json` dengan hashes,
`actual-projection.log`, `ollama-models.json` dan `ollama-profile.json`.
Inspeksi profil menunjukkan default system prompt Qwen umum, bukan instruksi
khusus menjawab pertanyaan hukum. Status resident mencatat context 8192 dan
sekitar 4,25 GB `size_vram`; ini bukan pengukuran peak VRAM. Tidak ada cloud call.

## Keputusan berikutnya

### Pemeriksaan dan eksperimen lanjutan pada baseline `dc99d46`

Pemeriksaan [kode template Ollama v0.32.1](https://github.com/ollama/ollama/blob/v0.32.1/template/template.go)
menunjukkan pesan system digabung saat rendering. Karena itu, template lokal
yang tidak mempunyai branch system dalam loop Messages saja tidak membuktikan
instruksi extractor dibuang. Tidak ada perubahan encoder produksi dari dugaan itu.

`run_contiguous.py` menguji prompt Indonesia yang membedakan surface form literal
dari canonical name, memakai source/schema/ontology, temperature 0 dan cap 4096
yang sama. Run selesai 115,594 detik, 2757 prompt + 2270 completion tokens,
finish `stop`. Output berisi 15 mentions, nol assertions/supports; **10 dari 15
locator tidak ada di sumber**. Replay produksi `TestQuotedExtractionSavedModelProjection`
gagal exit 1 dengan sepuluh field paths invalid. Prompt ini tidak dipromosikan;
JSON selesai tidak berarti fakta lengkap atau valid. Bukti berada pada
`contiguous-mention/actual-request.json`, `actual-response.json`, `result.json`,
`locator-diagnostic.json` dan `actual-projection.log`.

`run_literal.py` kembali memakai prompt Inggris v2, tetapi mengganti instruksi
envelope dan user content menjadi teks sumber literal dengan newline asli.
Source SHA/item ID dicatat pada `literal-source/source-identity.json`; metadata
ini bukan producer C01. Run selesai 219,25 detik, 2375 prompt + 4096 completion
tokens, finish `length`. Output tidak lengkap, sehingga tidak diterima; projection
dan kualitas semantik tidak diukur. Source JSON produksi tidak diubah. Bukti berada
di `literal-source/actual-request.json`, `actual-response.json`, dan `result.json`.

Kedua skrip HTTP exit 0 karena berhasil menyimpan respons, bukan karena extraction
PASS. Seluruh run dalam laporan ini telah terminal. Eksperimen tidak membuktikan
keunggulan suatu bahasa prompt, runtime, atau model secara umum.

### Tindak lanjut

Eksperimen contoh copying tidak dipromosikan karena menghasilkan repetition dan
truncation. Mengangkat cap saja tidak menyelesaikan pengulangan atau quote yang
tidak valid. Runtime CPU tersebut juga belum memberikan completion dalam batas
diagnostik. Semua kegagalan tetap dicatat; target required tidak berubah.

Pekerjaan berikutnya adalah memperbaiki profil/representasi ekstraksi secara
terukur, membuktikan satu item valid, lalu seluruh PDF dengan recovery completion
aktif. Setelah itu RESOLVE, graph/index publication dan query end-to-end harus
dibuktikan pada sumber nyata. Gold quality dan benchmark tetap belum diukur.
