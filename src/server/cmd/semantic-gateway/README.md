# src/server/cmd/semantic-gateway

`REGULAGRAPH_SEMANTIC_MAX_OUTPUT_TOKENS` menetapkan completion cap eksplisit
(default 4096) untuk setiap panggilan EXTRACT maupun RESOLVE. Nilainya harus
positif dan lebih kecil dari konteks model terpin. Cap dikirim sebagai `max_tokens`,
masuk fingerprint konfigurasi dan producer InputHashes, sehingga profil demo
provider tidak lagi menentukan batas secara diam-diam. Setelah mengganti cap,
ekspor producer baru dan submit job dengan pin baru; jangan ubah artefak historis.
Cap bukan pengukuran full prompt: prompt, schema, ontology, input dan completion
harus tetap muat dalam konteks provider. Ukur truncation, usage, latency serta
validitas output bersama; menaikkan cap tidak membuktikan akurasi atau throughput.

EXTRACT menyediakan ontology context terpin ke model sebagai pesan system kedua.
Ekspor producer kini membawa hash exact rendering, di samping schema/ontology
hash. Pasangkan gateway/coordinator baru dan ekspor ulang producer; pin lama
tanpa konteks tidak diterima EXTRACT. Context tidak berisi teks dokumen.

Startup juga memuat `configs/ontology-v1.jsonc` dengan SHA-256 dari environment. Proposal model yang memakai tipe, predicate, endpoint, origin, atau qualifier di luar vocabulary menjadi error per item; producer manifest mencatat hash ontology agar output dapat diaudit.

Entry point ini menjalankan layanan gRPC internal `Semantic.ExtractBatch`. Proses membaca prompt dan JSON Schema yang dipin, memverifikasi seluruh hash model, membuat satu client provider OpenAI-compatible, lalu memakai concurrency serta batas byte yang eksplisit. Model tidak dimuat atau client tidak dibuat ulang per item.

Mode `REGULAGRAPH_SEMANTIC_PRINT_PRODUCER=true` mencetak manifest ProtoJSON yang sama dengan producer response, lalu keluar sebelum listener/provider call. Simpan stdout UTF-8 tanpa BOM dan pin SHA-256 byte file untuk CLI submit/coordinator RESOLVE. Mode ini tetap memvalidasi konfigurasi startup; ia tidak memverifikasi kesiapan atau kualitas provider. Nonaktifkan mode print untuk menjalankan server.

Listener wajib loopback sampai autentikasi transport/TLS tersedia. Provider eksternal wajib HTTPS; provider HTTP hanya diterima pada loopback. API key hanya berasal dari environment dan tidak dimasukkan ke fingerprint atau log. Shutdown menunggu RPC aktif paling lama sepuluh detik sebelum menghentikan server.

Gateway mengimplementasikan EXTRACT atau RESOLVE sesuai task terpin. `SummarizeBatch` tetap unimplemented. Cache operation-key masih berada di memori proses, dibatasi jumlah operasi dan total byte; item sukses/error terminal dipakai kembali ketika item lain dalam batch perlu retry. Coordinator bertanggung jawab atas replay durable dan commit artefak lintas restart. Ketika kapasitas operasi penuh, request baru mendapat `ResourceExhausted` agar antrean/goroutine tidak tumbuh tanpa batas. Keakuratan ekstraksi dan target latency/biaya tetap **REQUIRED_UNMEASURED** sampai dijalankan dengan provider, model, corpus snapshot, dan evaluation split yang dibekukan.

`REGULAGRAPH_SEMANTIC_TASK=RESOLVE` memilih task resolusi dengan variabel model/schema berprefix `REGULAGRAPH_WORKER_RESOLUTION_`. Jalankan proses terpisah untuk EXTRACT dan RESOLVE agar model/prompt serta kapasitasnya dapat dipin sendiri. Default task tetap EXTRACT; task lain ditolak. [Panduan lokal dan batas integrasi](../../../../doc/semantic-resolution.md) menjelaskan setup dan prasyarat model.
