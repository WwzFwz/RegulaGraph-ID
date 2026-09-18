# Keputusan 0004: kode produk dikelompokkan dalam src

Dokumen ini mencatat persetujuan pengguna untuk mengumpulkan kode produk dan kontrak bersama di src, serta konfigurasi deployment di deployment. Perannya menetapkan batas folder tanpa mengubah pembagian runtime atau benchmark required.

Status: diterima berdasarkan arahan pengguna pada 2026-09-19. Lokasi apps/server menjadi src/server, workers/ingestion menjadi src/ingestion, native/inference menjadi src/inference, dan contracts menjadi src/contracts. Pembungkus apps, workers, serta native dihapus setelah anaknya dipindahkan. Compose berpindah dari root ke deployment/docker-compose.yml.

Server tetap Go, ingestion tetap Rust, inference tetap C++, dan evaluation/tooling tetap Python offline. Nama module, crate, namespace, serta package wire tidak berubah. Workspace manifest tetap di root. Path pada dokumentasi keputusan sebelumnya telah disesuaikan ke lokasi saat ini; keputusan ini menggantikan penempatan folder pada keputusan 0002, bukan pembagian tanggung jawab runtimenya.

Setiap folder sumber memiliki README tentang cakupan, integrasi anak, dan perhatian benchmark. Src memuat kode produk; deployment menentukan packaging dan wiring runtime. Library tidak otomatis menjadi layanan terpisah hanya karena memiliki folder sendiri. Tidak ada Dockerfile atau layanan aktif yang ditambahkan dalam perubahan struktur ini.

Path build, konfigurasi editor, dan tautan relatif diperbarui bersama pemindahan. CMake memakai direktori build baru .cache/inference-src agar cache yang menunjuk source lama tidak dipakai. Kontrak angka benchmark tetap sama; pemindahan folder tidak menjadi klaim peningkatan latency atau akurasi.

Lihat [cakupan src](../../src/README.md), [cakupan deployment](../../deployment/README.md), dan [peta repositori](../../README.md).
