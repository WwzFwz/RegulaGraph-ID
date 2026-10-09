# Jawaban lokal dari snapshot terpublikasi

Dokumen ini menjelaskan mode CLI yang menghubungkan retrieval empat profil dengan
generator lokal terpin dan validasi citation. [API HTTP](evidence-api.md) juga
menyediakan mode draft secara eksplisit dengan runtime model reusable.
Draft tetap UNREVIEWED/PARTIAL atau ABSTAIN, bukan pendapat hukum terverifikasi.

## Model dan konfigurasi

Jalankan llama.cpp lokal dengan GGUF terpilih, alias tetap, context window
eksplisit, context shift nonaktif dan API key lokal. Server harus memakai literal
loopback, model resident dan properties read-only. Binary yang diuji adalah
b11515; chat-compatible tidak otomatis berarti endpoint counting tersedia.
[Tokenisasi](generator-tokenization.md) menjelaskan invariant count/usage.

Siapkan JSON schema 1 lengkap berikut. Ganti placeholder hash/path dengan pin
artefak sebenarnya. `model` mengikuti C01 ModelManifest ProtoJSON. TokenizerHash
mengikat container GGUF yang juga memuat tokenizer. PromptHash harus sama dengan
`answering.DraftPromptHash()`. TemplateHash adalah SHA-256 UTF-8 string
`chat_template` yang dibekukan dari server.

```json
{
  "schema_version": 1,
  "corpus": "corpus:example",
  "endpoint": "http://127.0.0.1:55101",
  "gguf_path": "C:/absolute/path/model.gguf",
  "server_build": "b11515-3d65c90d0",
  "chat_template_sha256": "<64 lowercase hex>",
  "model": {
    "modelId": "regulagraph-local-qwen",
    "version": "<immutable model revision>",
    "weightsHash": {"sha256": "<GGUF SHA-256>"},
    "tokenizerHash": {"sha256": "<same GGUF SHA-256>"},
    "task": "MODEL_TASK_GENERATE",
    "maxTokens": 4096,
    "precision": "q4_k_m",
    "backend": "llama.cpp-cpu",
    "promptHash": {"sha256": "<draft system prompt SHA-256>"}
  },
  "allow_unreviewed_drafts": true,
  "limits": {
    "maximum_input_bytes": 1048576,
    "maximum_output_bytes": 65536,
    "maximum_claims": 8,
    "maximum_citations": 32,
    "maximum_concurrent": 1,
    "maximum_evidence": 64,
    "output_tokens": 256,
    "maximum_context_tokens": 2048
  }
}
```

Loader menolak duplicate/unknown fields, corpus/prompt/hash mismatch, credential
di URL, endpoint non-loopback, path relatif dan budget tidak valid. Secret tetap
environment. Angka contoh adalah konfigurasi runtime, bukan perubahan benchmark.
Context budget menyisakan ruang untuk prompt/schema/question dan output; exact
full-prompt admission tetap diperiksa sebelum generation.

## Menjalankan

Gunakan prasyarat [query evidence](query-evidence.md): PostgreSQL, FileStore,
Qdrant dan snapshot terpublikasi. Profil graph juga memerlukan Neo4j dan file
graph query terpin. Profil dense memerlukan embedding native sesuai katalog.
Graph-only tidak memerlukan embedding, tetapi mode jawaban memerlukan generator.

```powershell
$env:REGULAGRAPH_ANSWER_CONFIG = 'C:/absolute/path/answer.json'
$env:REGULAGRAPH_ANSWER_CONFIG_SHA256 = (Get-FileHash $env:REGULAGRAPH_ANSWER_CONFIG).Hash.ToLowerInvariant()
$env:REGULAGRAPH_ANSWER_API_KEY = '<local server key>'
go run ./src/server/cmd/cli query-evidence -answer -profile graph -question 'Apa dasar hubungan perizinan?' -as-of 2026-01-01 -limit 8 -timeout 3m
```

Tanpa `-answer`, generator config diabaikan dan output tetap evidence-only.
Dengan `-answer`, kegagalan generation tidak turun menjadi evidence success.
Envelope JSON berisi `mode: answer_draft`, `evidence`, `draft.answer` berupa C01
Answer, token usage, rejected candidates dan reranking jika diaktifkan. Satu
snapshot dipin dari retrieval sampai final validation. URL/span citation berasal
dari metadata sumber, bukan output model.

`maximum_evidence` harus menampung kandidat seluruh cabang (1 vector/graph,
2 hybrid, 3 hybrid-graph). Packing token tetap melaporkan bukti yang tidak muat.
Ctrl+C membatalkan request; CLI menutup koneksi dan lease dengan cleanup berbatas
waktu. Proses model/backend terpisah dan tidak dihentikan oleh CLI.

## Admission dan batas kepercayaan

Metadata `missing_required_evidence` di prompt memakai handle lokal seperti
`missing:1` dan jenis dependency, satu entri untuk setiap referensi yang hilang.
Urutan, jumlah, dan jenis tetap dipertahankan; ID canonical lengkap tetap berada
di ContextBundle dan Answer. Teks bukti, versi, dan ID citation yang tersedia
tidak diubah. Handle tidak boleh bertabrakan dengan ID bukti terpilih dan tidak
boleh dipakai sebagai citation. Ini mengurangi overhead identifier opaque tanpa
membuang dependency atau memperkecil jumlah kandidat retrieval.

Run manifest menambahkan hash payload, schema, dan daftar canonical omission
berurutan untuk mengikat proyeksi tersebut. Perubahan system prompt mengubah
`DraftPromptHash()`; konfigurasi lama harus dipin ulang secara eksplisit sebelum
dipakai binary baru. Full-prompt admission dan pemeriksaan usage tetap berlaku.
Pengurangan metadata bukan bukti kenaikan akurasi atau kelulusan benchmark.

Startup memeriksa GGUF regular file, magic, ukuran terbatas dan hash seluruh
container, lalu mencocokkan `/props`: normalized path, alias, build, template dan
context window. Pin dicatat pada producer manifest. Go tidak memuat model.

Warm guards memeriksa identitas file, ukuran dan mtime serta metadata server
sebelum full-prompt counting dan sebelum/sesudah generation. Hash multi-GB tidak
diulang per request; CountText tidak mengulang RPC properties per kandidat.
Drift yang terdeteksi menolak output. Server lokal/akun OS adalah boundary
tepercaya: ini bukan attestation RAM atau perlindungan terhadap administrator
yang memalsukan file stat dan respons server.

CLI cold invocation mengulang startup hash. Layanan warm memakai
`LocalAnswerRuntime` bersama setelah admission sekali. Jangan menyamakan cold CLI
dengan benchmark warm API. Uji native memakai corpus/alias sintetis dan Qwen
nyata; gold dan required performance tetap belum diukur.
