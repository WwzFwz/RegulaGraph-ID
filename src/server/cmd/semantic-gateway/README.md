# src/server/cmd/semantic-gateway

Entry point ini menjalankan layanan gRPC internal `Semantic.ExtractBatch`. Proses membaca prompt dan JSON Schema yang dipin, memverifikasi seluruh hash model, membuat satu client provider OpenAI-compatible, lalu memakai concurrency serta batas byte yang eksplisit. Model tidak dimuat atau client tidak dibuat ulang per item.

Listener wajib loopback sampai autentikasi transport/TLS tersedia. Provider eksternal wajib HTTPS; provider HTTP hanya diterima pada loopback. API key hanya berasal dari environment dan tidak dimasukkan ke fingerprint atau log. Shutdown menunggu RPC aktif paling lama sepuluh detik sebelum menghentikan server.

Gateway saat ini mengimplementasikan EXTRACT. `ResolveBatch` dan `SummarizeBatch` tetap unimplemented. Cache operation-key masih berada di memori proses, dibatasi jumlah operasi dan total byte; item sukses/error terminal dipakai kembali ketika item lain dalam batch perlu retry. Coordinator bertanggung jawab atas replay durable dan commit artefak lintas restart. Ketika kapasitas operasi penuh, request baru mendapat `ResourceExhausted` agar antrean/goroutine tidak tumbuh tanpa batas. Keakuratan ekstraksi dan target latency/biaya tetap **REQUIRED_UNMEASURED** sampai dijalankan dengan provider, model, corpus snapshot, dan evaluation split yang dibekukan.
