# Keputusan 0003: target benchmark numerik wajib

Dokumen ini mencatat arahan pengguna untuk menetapkan angka benchmark yang ambisius sebelum pengukuran. Implementasi yang belum memenuhi target harus terus diperbaiki; persetujuan hanya diperlukan untuk mengubah benchmark. Perannya menggantikan kebijakan menunda semua target sampai baseline tersedia.

Status: diterima berdasarkan arahan pengguna pada 2026-09-19; angka dipilih sebagai target desain, belum diukur. Target berada di [benchmark-targets.yaml](../../configs/benchmark-targets.yaml), suite regulagraph-performance-v1. Asumsi perangkat/workload dijelaskan dalam [panduan target](../benchmark-targets.md).

Target required berlaku pada profil referensi dan release Hybrid GraphRAG, dengan kualitas dan integritas sebagai syarat performa. Hardware pengguna belum ditentukan; profil memakai asumsi 16 core fisik, RAM 64 GiB, NVMe, GPU 24 GiB untuk embedding/reranker, dan generation endpoint terpisah. Tidak ada klaim bahwa perangkat saat ini sudah memenuhi target.

Developer/agent terus memperbaiki implementasi dan menguji ulang sampai target tercapai tanpa meminta izin untuk perbaikan dalam scope. Kegagalan tes tidak otomatis memicu negosiasi atau penghentian pekerjaan. Persetujuan pengguna diperlukan untuk perubahan benchmark, termasuk angka target, workload, asumsi penerimaan, dan kriteria lulus. Usulan perubahan harus menyertakan gap terukur, optimasi yang sudah dicoba, opsi perbaikan, serta dampaknya. Benchmark lama tetap berlaku sampai perubahan disetujui; perbaikan yang tidak terblokir tetap dilanjutkan. Setiap perubahan yang disetujui menggunakan versi suite baru dan mempertahankan hasil sebelumnya.

Keputusan ini menggantikan penundaan angka dalam dokumentasi scaffold sebelumnya. Ia tidak mengubah batas folder atau pilihan Go/Rust/C++/Python. Runner masih scaffold sehingga enforcement runtime belum tersedia; pengisian YAML tidak berarti benchmark sudah lulus.
