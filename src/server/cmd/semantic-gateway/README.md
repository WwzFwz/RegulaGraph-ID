# src/server/cmd/semantic-gateway

`REGULAGRAPH_SEMANTIC_REPLAY=postgres` mengaktifkan completion replay EXTRACT
lintas restart, memakai `REGULAGRAPH_POSTGRES_DSN` dan migration 0024. Default
`disabled` tetap cache memori. Producer/config berubah saat mode diaktifkan;
ekspor producer baru dan submit dengan pin baru. Mode print tidak membuka DB.
RESOLVE menolak opsi ini sampai replay resolusi tersedia. Store failure tidak
diam-diam fallback ke model; completion tersimpan tetap divalidasi ulang. Lihat
[kontrak dan cara menjalankan](../../../../doc/semantic-completion-replay.md).

Gateway menyediakan mode lokal `REGULAGRAPH_SEMANTIC_ADMISSION=llama.cpp` untuk
EXTRACT/RESOLVE. Set `REGULAGRAPH_SEMANTIC_GGUF_PATH` ke path absolut,
`REGULAGRAPH_SEMANTIC_LLAMA_BUILD` ke build server, dan
`REGULAGRAPH_SEMANTIC_TEMPLATE_SHA256` ke hash exact UTF-8 chat template.
Weights/tokenizer hash task wajib sama dengan hash container GGUF. Server harus
resident pada literal loopback, properties read-only, context window sesuai
manifest dan context shift dinonaktifkan. Endpoint `/v1/chat/completions/input_tokens`
wajib tersedia; server llama.cpp b11515 adalah kandidat yang telah dipakai jalur
answering. Kompatibilitas provider OpenAI saja tidak cukup untuk mode ini.

Startup memverifikasi byte GGUF dan metadata server. Sebelum setiap inference,
adapter menghitung envelope lengkap yang sama (system prompt, ontology context,
schema, item ID, source text), lalu menolak bila count + output cap melebihi
context window. Setelah inference, prompt usage harus sama dengan count awal;
mismatch menolak output. Tidak ada truncation atau fallback estimasi. Ukur biaya
counting/readiness bersama latency keseluruhan; ini bukan bukti akurasi model.

Default `provider` mempertahankan adapter generik, tanpa jaminan exact prompt
admission. Local pins yang diisi tanpa mode `llama.cpp` ditolak agar tidak diam-diam
diabaikan. Binding model/template/build/path masuk config fingerprint: ekspor
producer baru dan submit dengan pin baru setelah mengubahnya. Mode print hanya
mengekspor konfigurasi, tidak menghubungi/mengakui kesiapan server model.

Format quote v2 dapat dipilih dengan `REGULAGRAPH_SEMANTIC_PROMPT_PATH` menunjuk
`configs/prompts/extraction-v2.md` dan `REGULAGRAPH_WORKER_EXTRACTION_OUTPUT_SCHEMA`
menunjuk `src/contracts/jsonschema/extraction-output-v2.json`. Hitung ulang prompt
SHA-256, perbarui pin schema worker, ekspor producer baru, dan submit request baru.
Jangan mengganti schema saja sambil memakai prompt/model pins lama. Gateway
memilih projector quote dari `$id` schema v2 yang byte-nya dipin; v1 tetap tersedia.

`REGULAGRAPH_SEMANTIC_MAX_OUTPUT_TOKENS` menetapkan completion cap eksplisit
(default 4096) untuk setiap panggilan EXTRACT maupun RESOLVE. Nilainya harus
positif dan lebih kecil dari konteks model terpin. Cap dikirim sebagai `max_tokens`,
masuk fingerprint konfigurasi dan producer InputHashes, sehingga profil demo
provider tidak lagi menentukan batas secara diam-diam. Setelah mengganti cap,
ekspor producer baru dan submit job dengan pin baru; jangan ubah artefak historis.
Cap sendiri bukan pengukuran full prompt: mode lokal di atas menegakkan admission,
sedangkan mode provider masih memerlukan counter yang sesuai backend. Ukur truncation, usage, latency serta
validitas output bersama; menaikkan cap tidak membuktikan akurasi atau throughput.

EXTRACT menyediakan ontology context terpin ke model sebagai pesan system kedua.
Ekspor producer kini membawa hash exact rendering, di samping schema/ontology
hash. Pasangkan gateway/coordinator baru dan ekspor ulang producer; pin lama
tanpa konteks tidak diterima EXTRACT. Context tidak berisi teks dokumen.

Startup juga memuat `configs/ontology-v1.jsonc` dengan SHA-256 dari environment. Proposal model yang memakai tipe, predicate, endpoint, origin, atau qualifier di luar vocabulary menjadi error per item; producer manifest mencatat hash ontology agar output dapat diaudit.

Entry point ini menjalankan layanan gRPC internal `Semantic.ExtractBatch`. Proses membaca prompt dan JSON Schema yang dipin, memverifikasi seluruh hash model, membuat satu client provider OpenAI-compatible, lalu memakai concurrency serta batas byte yang eksplisit. Model tidak dimuat atau client tidak dibuat ulang per item.

Mode `REGULAGRAPH_SEMANTIC_PRINT_PRODUCER=true` mencetak manifest ProtoJSON yang sama dengan producer response, lalu keluar sebelum listener/provider call. Simpan stdout UTF-8 tanpa BOM dan pin SHA-256 byte file untuk CLI submit/coordinator RESOLVE. Mode ini tetap memvalidasi konfigurasi startup; ia tidak memverifikasi kesiapan atau kualitas provider. Nonaktifkan mode print untuk menjalankan server.

Listener wajib loopback sampai autentikasi transport/TLS tersedia. Provider eksternal wajib HTTPS; provider HTTP hanya diterima pada loopback. API key hanya berasal dari environment dan tidak dimasukkan ke fingerprint atau log. Shutdown menunggu RPC aktif paling lama sepuluh detik sebelum menghentikan server.

Gateway mengimplementasikan EXTRACT atau RESOLVE sesuai task terpin. `SummarizeBatch` tetap unimplemented. Cache operation-key masih berada di memori proses, dibatasi jumlah operasi dan total byte; item sukses/error terminal dipakai kembali ketika item lain dalam batch perlu retry. Completion EXTRACT dapat disimpan di PostgreSQL melalui opsi di atas; coordinator tetap memiliki checkpoint job dan commit artefak. Ketika kapasitas operasi penuh, request baru mendapat `ResourceExhausted` agar antrean/goroutine tidak tumbuh tanpa batas. Keakuratan ekstraksi dan target latency/biaya tetap **REQUIRED_UNMEASURED** sampai dijalankan dengan provider, model, corpus snapshot, dan evaluation split yang dibekukan.

`REGULAGRAPH_SEMANTIC_TASK=RESOLVE` memilih task resolusi dengan variabel model/schema berprefix `REGULAGRAPH_WORKER_RESOLUTION_`. Jalankan proses terpisah untuk EXTRACT dan RESOLVE agar model/prompt serta kapasitasnya dapat dipin sendiri. Default task tetap EXTRACT; task lain ditolak. [Panduan lokal dan batas integrasi](../../../../doc/semantic-resolution.md) menjelaskan setup dan prasyarat model.
