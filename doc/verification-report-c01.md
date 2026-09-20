# Verifikasi C01 dan handoff implementasi

Dokumen ini merangkum verifikasi kontrak C01, batas volume akuisisi D01, serta panduan kelanjutan pada 2026-09-20. Ia mencatat hasil pemeriksaan paket ini, bukan kelulusan seluruh Hybrid GraphRAG. Protokolnya berada di [verification](verification.md) dan petunjuk reproduksi di [contracts-implementation](contracts-implementation.md).

## Identitas bukti

Raw log, perintah, exit code, toolchain, hash fixture dan fingerprint file tersimpan lokal pada `artifacts/verification/c01-20260920/`. Manifest run menggunakan hash setiap file kode/schema/config yang diuji, sehingga tidak bergantung pada hash commit yang belum dibuat ketika tes dijalankan. File log/data lokal tidak dikirim ke Git; orang yang melakukan checkout ulang perlu menjalankan petunjuk C01 untuk mereproduksi hasil.

## Cakupan dan hasil

Fingerprint kode/schema/config final (SHA-256, line ending dinormalisasi): `bdba9959beb735742d2c542effe649974f11dae874bda5971e26bb117e46143d`.

| Pemeriksaan | Hasil | Batas pembuktian |
| --- | --- | --- |
| Codegen + baseline descriptor | PASS | Generated Go dapat direproduksi; 157 message, 31 enum, empat service descriptor |
| Go seluruh package + go vet | PASS | Termasuk guard citation/model/publication dan collector; layanan produksi belum aktif |
| Rust cargo test --locked --offline | PASS | Unit Unicode; interop default ignored dan dijalankan eksplisit oleh harness |
| C++ contracts dan inference scaffold | PASS | Library/probe dapat dibangun; belum inference model |
| Wire round-trip empat bahasa | PASS, 45 fixture | Valid/invalid, presence, unknown fields dan corpus wrapper; bukan matriks rilis historis lengkap |
| Python evaluasi | PASS, tiga tes perilaku | Split/duplikasi, prasyarat PASS, timing/rejected request; belum evaluator gate E01 |
| Header, README, tautan lokal, metadata dan diff | PASS | Satu temuan whitespace diperbaiki; raw log awal tetap disimpan |
| PDF lokal | PASS, 650 checksum | 2.999.240.002 bytes; bukan validasi sintaks PDF lengkap |
| Required benchmark produksi | NOT_MEASURED | Model, gold, runtime dan deployment belum siap |

Target benchmark YAML dibandingkan dengan baseline Git dan tidak berubah. Hasil build/fixture tidak dikonversi menjadi PASS untuk kualitas/latency produksi.

## Review independen

Agent terpisah `verify_c01` membaca kode dan dokumen serta menguji kasus tandingan. Temuan yang diperbaiki mencakup aturan artifact path/temporal/graph yang belum konsisten, Timestamp Go, enum/duplikasi worker stages, corpus pada batch dokumen dan wrapper semantic batch, serta citation URL/page locator yang harus berasal dari sumber tepercaya yang sama. Regression fixtures dan tes guard ditambahkan; reviewer mengulang 45 fixture binary pada Python serta tes Go citation/budget/collection. Reviewer tidak mengulang seluruh runtime sendiri; implementer menjalankan harness empat bahasa.

Review budget menemukan kegagalan HTTP dalam dokumen multi-PDF yang bisa tersamarkan oleh penghentian cap. Error non-budget kini diprioritaskan; reviewer menjalankan regression HTTP404 + overflow dengan hasil PASS. Audit event batch yang telah selesai tidak menemukan kasus mixed failure tersembunyi, sehingga ringkasan akuisisi historis tidak diubah. Reviewer menyatakan tidak ada temuan terbuka dalam cakupan terbatas ini.

## Keterbatasan dan tindak lanjut

Kontrak memiliki baseline awal dan unknown-field round-trip, belum matriks dua rilis historis. Go memiliki guard domain tambahan yang belum terhubung ke adapter/workflow produksi; layanan transport, database, model dan pipeline belum aktif. SourceSpan memerlukan pemetaan artefak teks ke blob/versi dan validasi teks nyata oleh I01/A01. Pemeriksaan struktural tidak membuktikan kebenaran hukum atau dukungan semantik klaim.

Required benchmark tetap NOT_MEASURED/REQUIRED_UNMEASURED: hardware deployment, gold lengkap, model serta runtime belum memenuhi prasyarat run acceptance. Angka `configs/benchmark-targets.yaml` tidak diubah. Lanjutkan E01 dan S01 menurut dependency di [implementation-guide](implementation-guide.md), dengan audit corpus D01/G01 yang sudah tersedia. Unduhan lokal berjumlah 650 PDF unik, 2.999.240.002 bytes, dan telah berhenti pada cap 3 GB; ini bukan corpus terstruktur atau gold dataset.

Arahan konkret tersedia pada header 97 file dan 40 README komponen. Modul initializer/generated bindings tidak perlu diisi algoritma tambahan. AGENTS.md mengikat pembacaan checklist dan review agent independen untuk paket perubahan bermakna berikutnya; dokumen tidak menjalankan enforcement otomatis.
