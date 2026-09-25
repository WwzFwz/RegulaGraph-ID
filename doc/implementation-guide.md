# Panduan melanjutkan implementasi

Dokumen ini memandu agent implementasi berikutnya, termasuk Sol 5.6, untuk merealisasikan seluruh rancangan RegulaGraph-ID secara terukur. Perannya menghubungkan dependency pekerjaan dengan rekomendasi pada header file dan README folder. Rekomendasi menjelaskan pekerjaan yang belum dilakukan; tidak boleh dibaca sebagai klaim bahwa fitur tersebut sudah tersedia.

## Mulai setiap sesi

Baca AGENTS.md, [development-plan](development-plan.md), [verification](verification.md), README seluruh induk folder yang akan diubah, dan catatan verifikasi terbaru. Periksa git status, diff, serta hasil tes aktual; konteks percakapan atau nama model bukan bukti status repo. Pertahankan perubahan pengguna. Jangan mengubah file generated secara manual atau menyalin algoritma produksi ke evaluator.

Untuk satu paket, tulis input–proses–output, kontrak yang dipakai, dependency yang benar-benar tersedia, invariant, resource budget, dan expected failure sebelum coding. Gunakan desain penuh yang telah disepakati. Urutan implementasi mengikuti dependency; tidak harus menuntaskan semua file dalam satu subtree lebih dahulu.

## Urutan pekerjaan yang disarankan setelah C01

| Paket | Hasil konkret | Syarat untuk melangkah |
| --- | --- | --- |
| E01 | Loader target/workload/manifest, raw observations, evaluator gate, telemetry | Invalid run/denominator kosong tidak menghasilkan PASS; pengukuran antrean/error ikut tercatat |
| S01 | Metadata/registry/job storage, artifact adapter, ledger, snapshot visibility, publication | Uji transaksi/retry/crash/fence dengan backend nyata; bukan hanya mock repository |
| D01/G01 persiapan | Audit corpus yang sudah diunduh, strata teks/scan/tabel/lampiran/perubahan, rencana label | Sampel punya provenance; split groups dan rantai rujukan dapat ditinjau manusia |
| M01 | Pembuktian parser/OCR dan model/native backend | Manifest dipin setelah hasil quality/parity/latency/memory tersedia |
| I01 | Parser, mapping, struktur, versi, chunk dengan parent | Output bisa ditelusuri ke PDF; halaman gagal dan tanggal ambigu eksplisit |
| K01/N01/X01 | Graph, native model service, indeks | Ikuti dependency spesifik development-plan; registry dan representasi tidak bercabang |
| Q01/A01/U01 | Retrieval, jawaban bersumber, update incremental | Evidence dan historical versions konsisten; failure paths teruji |
| O01/B01 | Deployment/operasi dan acceptance menyeluruh | Semua required applicable lulus pada profil yang disepakati |

E01 dan S01 dapat dikerjakan sebagai pekerjaan terpisah setelah kontrak terkait tersedia; setiap hasil integrasi tetap memiliki owner. Tidak ada instruksi menjalankan banyak agent implementasi tanpa kebutuhan; agent verifikasi independen mengikuti otorisasi dan aturan verification.md.

## Cara membaca rekomendasi per file

Header file menyatakan langkah berikutnya, dependensi/integrasi, dan bukti selesai yang spesifik. README folder merangkum integrasi antar anak serta risiko utama komponen. Berkas module initializer hanya mengekspos submodul/types; tidak perlu diisi algoritma. Berkas build/codegen perlu reproducibility, bukan logika bisnis. Folder kosong seperti migrations diisi sesuai paket pemiliknya setelah desain storage diterjemahkan.

Rekomendasi adalah panduan teknis dalam scope yang disetujui, bukan izin mengganti target atau memperluas fungsi folder. Jika temuan nyata mengubah pilihan implementasi, jelaskan bukti dan dampak, perbarui dokumentasi/decision yang relevan, lalu verifikasi. Hanya perubahan benchmark/ruang lingkup yang memerlukan persetujuan sesuai aturan repositori; perbaikan bug/optimasi dalam scope sudah diotorisasi.

## Prinsip implementasi yang tidak boleh hilang

Go mengatur request/workflow, fusion/filter/context/citation, penjadwalan, dan publication. Rust menghasilkan batch transformasi dokumen/graph/index. C++ mengelola sesi inference/tokenization/batching. Python dipakai offline untuk evaluasi/tooling. Jangan menambah LangGraph Python pada jalur request atau memindahkan extraction graph ke waktu query.

Chunk mengikuti struktur dan membawa konteks induk. Canonical identity bukan daftar sinonim tanpa scope; ambiguity, merge/split, dan revision harus terlacak. Tanggal hukum berbeda dari waktu observasi serta knowledge snapshot. Pembaruan harus mempertahankan historical versions dan bukti bersama, serta mencatat dependency negatif. Optimasi harus menjaga kualitas; jangan mengurangi kandidat/hop/kasus sulit tanpa evaluasi dampak dan pelaporan konfigurasi.

Angka benchmark berasal satu kali dari configs/benchmark-targets.yaml. Jangan menyebut kecepatan “sangat baik” sebagai hasil sebelum pengukuran. Jika data/hardware/model belum siap, laporkan NOT_MEASURED/BLOCKED untuk gate terkait dan lanjutkan pekerjaan independen. Seluruh temuan agent verifikasi yang berada dalam paket aktif harus diperbaiki dan diuji sebelum klaim selesai.

## Serah terima setiap paket

Catat perubahan perilaku, file pemilik, perintah verifikasi, hasil/exit code, raw log, keterbatasan, serta pekerjaan selanjutnya. Sebutkan apakah schema, helper, adapter, atau layanan benar-benar sudah aktif. Jangan menjadikan komentar TODO yang banyak sebagai pengganti implementasi ataupun laporan keberhasilan.

## Titik lanjut resolusi kontekstual

Gateway `Semantic.ResolveBatch`, client reusable, workflow `ProposeWithModel`, planner kandidat lintas scope, loader policy terpin, CLI submit durable, serta katalog/hidrasi bukti kandidat lintas dokumen tersedia. Lihat [semantic-resolution.md](semantic-resolution.md): nilai scope produksi, dispatch RESOLVE daemon, keputusan canonical baru/merge/split, serta acceptance model lokal masih perlu diselesaikan. Smoke Ollama menguji protokol pada fixture sintetis; proposal model bukan review tersimpan.
