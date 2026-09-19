# Keputusan 0005: desain sistem lengkap sebelum implementasi

Dokumen ini mencatat arahan pengguna untuk merancang seluruh sistem sejak awal, termasuk kontrak maksimal sesuai cakupan produk, sebelum memecah implementasi menurut dependency. Perannya menggantikan usulan memulai hanya dari kontrak minimum. Status: arahan cakupan diterima pada 2026-09-19; mekanisme teknis dalam spesifikasi adalah baseline desain yang perlu dibuktikan, bukan implementasi atau hasil benchmark.

Pengguna memilih Database Peraturan BPK, JDIH Kemkomdigi, dan JDIHN Nasional sebagai sumber corpus. Pilihan portal tidak membatasi sistem pada satu topik regulasi. Akuisisi, duplikasi, metadata konflik, perubahan versi, provenance, serta kurasi gold dirancang bersama pipeline.

Baseline mencakup [system-design](../system-design.md), [system-contracts](../system-contracts.md), [storage-consistency](../storage-consistency.md), [corpus-plan](../corpus-plan.md), dan [development-plan](../development-plan.md). Desain mengatur runtime ownership, seluruh domain/RPC/API/event, staged publication, snapshot pinning, registry/history, dependency invalidation, failure recovery, deployment, serta evaluasi.

Go/Rust/C++/Python dan struktur src tetap mengikuti keputusan 0002/0004. gRPC batch dipilih sebagai rancangan transport internal; field/tag/codegen belum diimplementasikan. Endpoint C++ dan worker executable juga belum tersedia. Seluruh realisasi wire schema adalah paket C01, bukan pekerjaan parsial yang dianggap selesai melalui dokumen.

Snapshot menggunakan visibility revisions dan publisher serial per corpus dengan ledger/CAS. Takeover harus menjamin writer lama tidak dapat merusak compensation; HA otomatis tidak diklaim tanpa pembuktian tersebut. Statistik BM25 dipin per generation agar perubahan staged corpus tidak diam-diam mengubah scoring snapshot; kualitas drift serta biaya refresh wajib diuji.

Desain lengkap tidak mengarang pilihan hardware, kecocokan model/backend, isi hukum, hasil pengukuran, atau kelengkapan sumber. Semua target required dari keputusan 0003 tetap sama. Implementasi mengikuti dependency, disertai bukti unit/integrasi/failure/benchmark, dan perubahan benchmark tetap memerlukan persetujuan pengguna.
