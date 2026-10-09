# Verifikasi admission full prompt semantic lokal

Dokumen ini mencatat pemeriksaan batas konteks EXTRACT/RESOLVE pada adapter
llama.cpp dan wiring gateway. Ia membedakan kontrol runtime yang diuji dari
kualitas ekstraksi corpus yang belum tercapai. Target benchmark tidak berubah.

## Implementasi dan kontrak

Mode `REGULAGRAPH_SEMANTIC_ADMISSION=llama.cpp` memerlukan pin GGUF absolut,
hash container yang sama untuk weights/tokenizer, build server, chat template,
dan manifest model. Startup melakukan hashing file serta pemeriksaan metadata
server resident/read-only pada literal loopback. Gateway memakai satu client
HTTP reusable dan menutupnya setelah server berhenti. Model tidak dimuat per item.

EXTRACT/RESOLVE memakai encoder chat yang sama untuk counting dan generation,
termasuk system prompt, ontology context, schema, item ID, dan source text.
Syarat admission adalah `input_tokens + maximum_output_tokens <= model.max_tokens`.
Input oversize ditolak tanpa truncation atau retry. Prompt hash dan cap diperiksa,
metadata model diperiksa sebelum count, sebelum generation, dan sesudahnya;
reported prompt usage harus sama dengan preflight count agar output diterima.
Service menolak manifest yang berbeda dari provider lokal admitted.

Binding lokal masuk config fingerprint producer. Ekspor producer adalah deklarasi
konfigurasi saja, tanpa hashing model/server call. Mode generik `provider` tetap
tersedia dan tidak mempunyai jaminan exact counting. Local pins tanpa mode lokal
ditolak. Lihat [konfigurasi gateway](../src/server/cmd/semantic-gateway/README.md).

## Bukti

Base revision `ba0a1571abe9cd29dbcd22cdeacd7b7e2df64a03` ditambah diff paket ini;
fingerprint file final berada pada `independent-results.json`. Toolchain Go
1.26.8 windows/amd64. Semua log berada di
`artifacts/verification/20261009-semantic-admission/` (artefak lokal diabaikan Git).

| Pemeriksaan | Hasil | Bukti dan batas |
| --- | --- | --- |
| `go test ./src/server/internal/adapters/inference ./src/server/cmd/semantic-gateway ./src/server/internal/answering ./src/server/internal/workflows` | PASS, exit 0 | `regressions.log`; integrasi opt-in tanpa env tetap SKIP |
| `go vet` atas empat paket yang sama | PASS, exit 0 | `vet.log` |
| Review independen + ulang tes adapter/gateway `-count=1 -v` | PASS_SCOPED, exit 0 | `independent-results.json`, `independent.log`; fixture bukan kualitas model |
| Native opt-in `TestSemanticLlamaNativeAdmission` | PASS, exit 0 | `native.log`; exact usage parity dan overflow rejection pada model nyata |
| Full-PDF EXTRACT, kualitas/legal correctness, benchmark required | NOT_MEASURED dalam paket ini | Kegagalan extraction sebelumnya tetap terbuka |

Kasus negatif mencakup overflow satu token, missing cap, prompt/model drift,
counter unavailable, drift antara count dan generation, cancellation, mismatch
usage, mode tidak dikenal, pin lokal yang diabaikan, serta relative GGUF path.
Batas tepat 4096 token diterima pada kedua task dalam fixture. Tidak ada output
yang diterima setelah guard gagal.

## Run model nyata

Server lokal llama.cpp `b11515-3d65c90d0`, CPU 4 threads, 1 slot, context 8192,
GPU layers 0, context shift nonaktif. Hash GGUF Qwen2.5 7B:
`2bada8a7450677000f678be90653b85d364de7db25eb5ea54136ada5f3933730`.
`server-process.json`, `server-props.json`, binary hashes, serta stdout/stderr
merekam proses dan konfigurasi. Alias run `regulagraph-semantic-test`.

Synthetic source `Badan wajib izin.` memakai schema salin-teks kecil dan trusted
system context. Full prompt berjumlah 65 token, output 13 token; preflight count
sama dengan usage. Teks panjang berikutnya ditolak dengan `context_window`
sebelum generation. Tes selesai dalam 10.57 detik termasuk admission/model hash;
angka ini bukan p95 atau benchmark throughput. Proses server milik run dihentikan
setelah tes, tanpa menghentikan Ollama atau backend pengguna.

Reproduksi memakai `REGULAGRAPH_TEST_SEMANTIC_LLAMA=1` dan pin
`REGULAGRAPH_TEST_LLAMA_ENDPOINT`, `MODEL`, `BUILD`, `GGUF`, `SHA256`,
`TEMPLATE_SHA256`, serta `KEY` bila server memerlukannya (semua berprefix
`REGULAGRAPH_TEST_LLAMA_`). Jalankan
`go test ./src/server/internal/adapters/inference -run '^TestSemanticLlamaNativeAdmission$' -count=1 -v`.
Gunakan server context 8192 dengan model resident dan endpoint counting tersedia.

## Batas dan pekerjaan berikutnya

Pin file/properties adalah pemeriksaan trusted local process, bukan attestation
RAM. Context-shift nonaktif masih prasyarat operator. Counting dan readiness
menambah RPC; latency/antrean harus diukur pada workload sebenarnya. Cache semantic
masih in-memory, recovery per-item lintas restart belum tersedia. Model copying
smoke tidak membuktikan extraction ontology, resolusi entitas, atau graph lengkap;
uji PDF dan gold tetap dibutuhkan. Dua eksperimen feedback gagal sebelumnya tetap
tercatat pada [laporan extraction](verification-report-extraction-feedback.md).
