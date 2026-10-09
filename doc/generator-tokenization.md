# Tokenisasi generator lokal

Dokumen ini menjelaskan penghitung token untuk generator jawaban dan batas bukti
integrasinya. Model, prompt, chat template dan tokenizer harus diikat sebelum
menilai kualitas/performa. Ini belum panduan command jawaban operasional.

`inference.LlamaTokenCounter` memakai client generation yang sama untuk URL,
credential, timeout dan larangan redirect. `CountPrompt` mengirim body identik
ke `/v1/chat/completions/input_tokens`; `Generate` memakai `/v1/chat/completions`.
Keduanya memakai encoder `llm_request.go`, termasuk system prompt, JSON yang
membungkus teks bukti, schema output dan output reserve. Jika endpoint counting
tidak tersedia, operasi gagal; tidak ada perkiraan kata/byte atau fallback model.

`CountText` memakai `/tokenize` untuk packing konteks tanpa automatic BOS, dengan
parsing special token aktif. Count konteks ini bukan count seluruh chat. Schema
dan envelope tetap dihitung kembali oleh endpoint complete-prompt. Endpoint
tersebut disediakan llama.cpp; dukungan chat-compatible saja, termasuk pada
provider lain, tidak menjamin kemampuan counting ini. Lihat
[dokumentasi server llama.cpp](https://github.com/ggml-org/llama.cpp/blob/b11515/tools/server/README.md).

Admission generator mensyaratkan `prompt_tokens + output_reserve <= context_window`
dan usage aktual harus persis sama dengan count prompt. Truncation, count nol
untuk prompt, accounting invalid, response oversized dan cancellation menjadi
error. Array token dibaca per ID dengan batas jumlah sehingga banyak ID pendek
tidak memicu alokasi slice besar. Duplicate keys, null, ID negatif dan noninteger
ditolak. Batas bytes berlaku setelah serialisasi, termasuk ekspansi escape JSON.

Sebelum wiring runtime, operator masih perlu memverifikasi GGUF, build server,
model alias, template dan context window. Counter sendiri **tidak membuktikan
identitas model** dari respons hitungan. Pada GGUF, tokenizer tertanam dalam
container; pin container hash mengikat bytes tokenizer beserta bobotnya, sedangkan
template runtime tetap harus dipin terpisah. Native test membaca ulang hash GGUF;
startup server dan properti disimpan di laporan, bukan admission produksi.

Uji lokal opt-in berada di `answering/generator_native_test.go`, diaktifkan dengan
`REGULAGRAPH_TEST_LLAMA=1` dan suffix environment `_ENDPOINT`, `_MODEL`, `_BUILD`,
`_GGUF`, `_SHA256`, serta `_KEY` bila server memakai credential. Endpoint harus
literal loopback. Jalankan
`go test ./src/server/internal/answering -run '^TestNativeLlamaCitedDraft$' -count=1 -v`.
Fixture evidence sintetis diteruskan ke packing, model nyata, citation dan final
validation. Ini tidak menguji ingestion/retrieval corpus nyata atau semantic gold.

Packing saat ini menghitung trial konteks lewat RPC untuk setiap kandidat, lalu
satu count full prompt. Ukur overhead ini bersama generation, queue dan p95/p99;
jangan menjumlahkan token tiap blok sebagai pengganti tokenisasi teks gabungan.
Optimasi batching/cache tokenizer berikutnya harus mempertahankan parity model,
template dan prompt. Angka required tetap di `configs/benchmark-targets.yaml`.
