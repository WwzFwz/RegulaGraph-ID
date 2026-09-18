# Keputusan 0002: runtime Go, Rust, C++, dan Python offline

Dokumen ini mencatat persetujuan pengguna untuk memigrasikan scaffold produksi dari Python ke beberapa runtime berdasarkan tanggung jawab komponen. Perannya menetapkan cakupan folder baru dan menggantikan pilihan runtime pada keputusan 0001.

Status: diterima pengguna dan diterapkan pada struktur scaffold, 2026-09-18. Latency dan throughput menjadi prioritas bersama kualitas bukti. Perangkat serta skala deployment belum ditentukan. Implementasi pipeline belum tersedia.

## Keputusan dan batas folder

src/server menampung Go untuk serving, workflow, retrieval, answering, coordinator ingestion, serta adapter database. src/ingestion menampung Rust untuk transformasi dokumen/graph/index batch. src/inference menampung C++ untuk wrapper model runtime. src/contracts menjadi sumber wire schema lintas bahasa. evaluation dan tooling tetap Python offline.

Go memiliki penjadwalan dan publikasi state. Rust menghasilkan batch beserta manifest dependency. Inference native menjalankan model dan tidak mengambil alih kebijakan ranking/answering. Python mengevaluasi runtime melalui endpoint/artefak bersama, bukan menggandakan serving pipeline.

Pengguna telah menyetujui perubahan foldering berdasarkan kajian runtime. Persetujuan ini mencakup penggantian src/regulagraph yang hanya berisi docstring scaffold dan pembaruan manifest/dokumentasi. File evaluasi yang tidak perlu berubah, referensi pengguna, serta PDF sumber dipertahankan.

## Konsekuensi

Source Python produksi tidak dipelihara sebagai implementasi paralel. Fungsi fungsional tetap dipisahkan di dalam runtime pemiliknya. Semua folder memiliki README, dan semua file kode mempunyai deskripsi fungsi, integrasi, serta benchmark/perhatian performa pada bagian atas.

Fusion/filter/context/citation tetap satu proses Go. Parsing native menggunakan boundary Rust ke engine C/C++; tidak perlu menulis parser dari nol. C++ scaffold belum memilih ONNX SDK atau model dan belum menyediakan server inference.

## Kontrak dan benchmark

Schema wire berada di src/contracts/proto/regulagraph/v1. Deklarasi awal belum membekukan field atau service. Offset teks yang dipertukarkan akan menggunakan byte UTF-8 start-inclusive/end-exclusive dengan identitas teks dan mapping sumber. Pengujian parity serta source/version identity berlaku lintas bahasa.

Build scaffold hanya membuktikan layout serta syntax/kompilasi. Pengukuran p95/p99, waktu antre, TTFT, throughput, dan kualitas memerlukan model, corpus, serta workload nyata. Ikuti [kebijakan benchmark](../benchmark-policy.md).

## Hubungan keputusan

Keputusan ini menggantikan pemilihan paket produksi Python dan asumsi FastAPI pada [0001](0001-scaffold-boundaries.md). Keputusan chunk struktural, konteks induk, canonical identity, pemilihan versi, pemrosesan incremental, serta aturan konfirmasi cakupan folder tetap berlaku.


Kebijakan angka benchmark pada catatan historis ini telah diperbarui oleh [keputusan 0003](0003-required-benchmark-targets.md): target numerik kini required pada profil referensi asumsi, dengan hasil belum diukur.
