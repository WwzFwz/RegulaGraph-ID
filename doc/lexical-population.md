# Persiapan populasi lexical dari chunk terverifikasi

Dokumen ini menjelaskan alur operator untuk menghitung vocabulary dan statistik
BM25 dari teks yang benar-benar dipakai INDEX. Rust memegang rendering/analyzer;
Go mengalokasikan ID term dalam PostgreSQL. Alur ini menyiapkan artefak, bukan
menyetujui membership corpus, menjalankan scheduler INDEX, atau mempublikasikan
snapshot.

## Kontrak dua pass

`population.rs::prepare_population` menerima daftar ArtifactRef DocumentBatch
lengkap, SnapshotRef dan auth scope yang dibekukan. Byte sumber/text/mapping
diverifikasi; snapshot/scope harus sama persis. Semua chunk dihitung tepat sekali.
Identitas chunk yang muncul lagi pada sumber lain ditolak, tidak dideduplikasi
diam-diam. Renderer `structure-labels-v1` serta analyzer lexical v1 sama dengan
worker INDEX, termasuk label struktur dalam panjang dokumen dan DF.

Pass pertama mengeluarkan vocabulary unik terurut sebagai baris UTF-8 berakhiran
LF. Go `prepare-dictionary` memverifikasi hash byte dan canonical analyzer terms,
mengalokasikan term dalam halaman 5.000 melalui registry, mengekspor revision
historis terpin, lalu menyimpan/mendaftarkan dictionary immutable. Operation key
mengikat seluruh vocabulary, corpus/analyzer dan posisi halaman. Retry vocabulary
yang sama memulihkan registrasi tanpa mengganti ID, bahkan setelah alokasi lain.

Pass kedua membaca ulang sumber immutable yang sama, memakai dictionary hasil
Go dan membentuk LexicalStatisticsArtifact C01. `input_policy` hanya diisi oleh
producer yang memang merender populasi tersebut. N, total tokens, zero-token
documents dan fingerprint berasal dari populasi aktual; tidak dipasok manual.
Term yang belum dialokasikan menggagalkan freeze. Zero-token chunks tetap masuk
denominator; populasi seluruhnya kosong ditolak saat freeze. Worker INDEX sendiri
masih menolak zero-term selection secara eksplisit.

## Perintah operator

Jalankan `cargo build -p regulagraph-ingestion --bin regulagraph-lexical --locked --offline`.
Sediakan snapshot dan setiap ArtifactRef sumber sebagai **protobuf biner C01**,
bukan JSON. Artefak yang ditunjuk sudah berada di root store bersama. Inventory
harus dipilih/diautorisasi oleh coordinator dan terikat checkpoint CHUNK sukses;
command tidak menyimpulkannya dari file PDF atau isi direktori.

```powershell
target/debug/regulagraph-lexical.exe vocabulary --artifacts data/artifacts --snapshot snapshot.pb --auth-scope operator:corpus --source-ref source-ref.pb --output vocabulary.txt
go run ./src/server/cmd/cli prepare-dictionary -vocabulary vocabulary.txt -vocabulary-sha256 <pin-byte-vocabulary> -corpus corpus:example -output dictionary-ref.pb
target/debug/regulagraph-lexical.exe freeze --artifacts data/artifacts --snapshot snapshot.pb --auth-scope operator:corpus --source-ref source-ref.pb --dictionary-ref dictionary-ref.pb --statistics-id statistics:example --k1 1.2 --b 0.75 --output statistics-ref.pb
```

Go membutuhkan `REGULAGRAPH_POSTGRES_DSN` dan `REGULAGRAPH_ARTIFACTS_DIR` menuju
store yang sama; migrasi database harus sudah diterapkan. Parameter k1/b contoh
adalah konfigurasi algoritma eksplisit, bukan angka acceptance yang boleh diubah.
Ulangi `--source-ref` untuk semua sumber; dictionary ancestry dapat diberikan
root-ke-child dengan `--dictionary-ref` berulang. Go exporter menghasilkan root
snapshot revision yang dipilih, tanpa mengklaim ancestry sebelumnya.

Output dictionary/statistics adalah **ArtifactRef biner** pada file baru; payload
ada di store content-addressed. Command menolak overwrite. Kegagalan dapat
meninggalkan file output kosong/parsial dan alokasi/artefak immutable yang sudah
durable; periksa exit code dan gunakan nama output baru saat replay. Exit 0 berarti
persiapan berhasil, bukan publication. Rust command memakai normalizer default;
artefak yang tidak cocok dengan normalisasi ulang ditolak.

## Batas integrasi dan performa

Population preparation membatasi 256 sumber, 32.768 chunk, 64 MiB source bytes,
2 juta tokens dan 64 MiB akumulasi term bytes. Setiap raw/normalized/mapping
dibatasi 16 MiB; total text read dibatasi 512 MiB termasuk pembacaan ulang lintas
halaman. Batas hitungan/bytes menahan kerja tetapi bukan angka peak RSS. Render
tetap maksimal 128 chunk/2 MiB per pilihan. Batas yang terlampaui menjadi error;
tidak mengurangi populasi agar lolos. Ukur reread/normalization dan RSS untuk
merencanakan cache atau streaming pada workload besar.

Go dictionary export dibatasi kapasitas wire (49.999 terms dengan default limits).
Registry dapat memiliki vocabulary lebih besar; bila export melampaui batas,
hasil bukan dictionary parsial. Semua artefak, source refs dan analyzer/dictionary
dependencies harus diregistrasikan sebelum PlanInitialIndex menerima generation.
Rust CLI menulis statistik tetapi tidak mendaftarkannya ke PostgreSQL; scheduler
Go tetap harus memverifikasi hasil dan menyimpan dependencies tersebut. Durable
inventory/child-job dispatch dan pengikatan seluruh statistik ke publication masih
pekerjaan lanjutan. Tidak ada perubahan target required atau klaim benchmark PASS.

Lihat [verifikasi populasi](verification-report-lexical-population.md),
[kontrak lexical](lexical-generation.md), dan [worker INDEX](index-build.md).
