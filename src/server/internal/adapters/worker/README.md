# src/server/internal/adapters/worker

Client Go untuk pekerjaan batch di worker Rust, termasuk status dan referensi artefak. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Kirim locator/hash/snapshot dan batch descriptor; hindari RPC per karakter/chunk kecil. Client memakai generated gRPC C01, menerapkan deadline yang lebih awal, lalu memvalidasi response attempt/fence sebelum coordinator menerima output.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [client.go](client.go), [client_test.go](client_test.go), dan [client_integration_test.go](client_integration_test.go). Integration test lintas proses hanya berjalan ketika alamat worker serta artifact root diberikan melalui environment.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Client `ProcessBatch`, `GetStatus`, dan `Cancel` aktif dengan validasi wire, deadline caller/body yang paling awal, transport credentials eksplisit, message bound, serta pemeriksaan response stale. Integration test environment-gated menjalankan client Go terhadap executable Rust melalui HTTP/2 loopback dan shared artifact root. Worker Rust melayani tahap PARSE, tetapi scheduler/workflow Go belum otomatis membentuk dan menyerahkan batch. Graph/index, layanan model, gold dataset, dan acceptance produksi belum aktif.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [client.go](client.go) | Wire client ke executor scheduler setelah artifact source dan lease durable tersedia; tambahkan observability RPC/queue. | Uji disconnect/retry lintas proses, corrupted artifact refs, lease renewal/cancellation, dan message limit nyata; hindari RPC per chunk. |
