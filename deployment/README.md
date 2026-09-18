# deployment

Folder ini menampung resep packaging, konfigurasi lingkungan deployment, dan wiring antar artefak runtime RegulaGraph-ID. Perannya menjelaskan bagaimana hasil build server, pemrosesan ingestion, inference, serta layanan penyimpanan dijalankan bersama. Algoritma produk berada di src; schema database berada di migrations; parameter aplikasi dan benchmark berada di configs.

## Peran dan integrasi anak

[docker-compose.yml](docker-compose.yml) adalah deklarasi awal deployment lokal dengan services kosong. Belum ada Dockerfile, image aplikasi, endpoint aktif, atau transport worker yang dapat dijalankan. Resep deployment berikutnya harus menggunakan artefak yang benar-benar tersedia dan menjelaskan entry point, dependency runtime, model, health check, resource limit, serta cara memasok rahasia dari luar version control.

Saat menambah konfigurasi Compose, periksa kembali resolusi path relatif terhadap folder deployment: build context yang memakai root repo harus menunjuk ke direktori induk, dan volume/config harus menunjuk lokasi yang benar. Jalankan Compose dari root dengan argumen file eksplisit deployment/docker-compose.yml setelah layanan tersedia. Jangan menganggap file .env di root dimuat otomatis oleh semua cara menjalankan Compose.

Packaging menentukan file yang dibutuhkan setiap artefak. Evaluation, tooling, dan tests dijalankan dalam lingkungan pengembangan atau job tersendiri, tidak perlu menjadi dependency image server. Binding contracts dibangun dari sumber schema yang sama; migrasi database dijalankan sebagai langkah operasional yang jelas. Jangan memuat model lewat handler request atau membiarkan job ingestion menghabiskan kapasitas query.

## Benchmark dan batas cakupan

Resource limit, versi runtime/model, penempatan database, jaringan, dan kebijakan antrean harus tercatat agar [benchmark required](../configs/benchmark-targets.yaml) dapat direproduksi. Ikuti [kebijakan benchmark](../doc/benchmark-policy.md); deployment yang belum berjalan tidak mempunyai hasil latency atau throughput. Perubahan benchmark memerlukan persetujuan pengguna, sementara perbaikan implementasi dalam scope tetap dilanjutkan.

Anak baru wajib mendokumentasikan peran dan integrasinya. Fungsi di luar packaging/deployment mengikuti komponen pemiliknya; perubahan cakupan mengikuti [AGENTS.md](../AGENTS.md). Folder ini dan pemindahan Compose telah disetujui pengguna.
