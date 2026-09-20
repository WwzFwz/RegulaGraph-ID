# Akuisisi metadata dan PDF otomatis

Dokumen ini menjelaskan collector D01 yang sudah dapat dijalankan dari CLI Go. Perannya mengumpulkan file PDF nyata, HTML sumber, metadata portal, checksum, dan receipt akuisisi. Collector belum menjalankan parser isi PDF, OCR, chunking, canonical registry, graph, atau publication produksi; schema inventory lokal tidak menggantikan seluruh kontrak C01.

## Keputusan sesi 2026-09-20 dan antrean untuk dilanjutkan

Pengguna mengubah target awal dari 5 GB menjadi **3 GB PDF unik (3.000.000.000 bytes total, termasuk PDF valid yang sudah tersimpan)**, meminta daftar sumber diperbanyak dahulu, dan menunda unduhan PDF lanjutan sampai besok. Tidak ada penjadwalan otomatis. Angka ini target volume akuisisi, bukan perubahan required benchmark. Saat ini CLI collect belum memiliki penghentian otomatis berdasarkan total bytes; sebelum batch 3 GB dijalankan, tambahkan serta uji batas volume tersebut. Jangan menjalankan seluruh antrean tanpa batas dan menganggapnya berhenti sendiri pada 3 GB.

Mode berikut hanya membaca halaman katalog dan menyimpan daftar; tidak menjalankan collect:

```powershell
go run ./src/server/cmd/cli discover -input configs/listings.txt -max-pages 200 -interval 1s -out data/acquisition
```

Discovery menghasilkan `discovery.json` berisi seed, halaman yang sudah dibaca, judul tautan, provenance HTML, dan error; `queue.txt` berisi URL detail unik siap menjadi input collect. Checkpoint disimpan setiap halaman dan export dapat dibangun ulang jika proses terputus. Run ulang menelusuri checkpoint untuk mengambil halaman yang belum selesai, dengan batas 200 halaman **baru per run**, bukan per seed. Jalankan satu penulis saja untuk direktori yang sama. Tidak ada klaim seluruh katalog sudah tercakup.

Seed mencakup katalog Kemkomdigi, BPK berdasarkan jenis/tahun, serta homepage JDIHN. BPK memakai nomor halaman berurutan karena label Next pada layout teramati melompati kelompok sepuluh halaman. Discovery JDIHN mengenali tautan /doc/ di homepage; search JavaScript dan unduhan portal anggota belum tersedia. Judul berasal label tautan dan belum menggantikan metadata halaman detail. URL unik lintas portal belum berarti peraturan kanonis unik.

Untuk kelanjutan besok: baca checkpoint/error, aktifkan batas total 3 GB terlebih dahulu, lalu gunakan queue.txt dengan resume checksum. Pertimbangkan timeout lebih panjang untuk PDF besar: smoke BPK UU 6/2023 sebelumnya gagal saat membaca body melewati timeout 60 detik. PDF berhasil yang sudah ada tetap disimpan.

## Menjalankan unduhan

Dari root repositori, jalankan contoh sumber BPK dan Kemkomdigi yang tersimpan pada [sources.txt](../configs/sources.txt):

```powershell
go run ./src/server/cmd/cli collect -input configs/sources.txt
```

PDF disimpan di `data/acquisition/blobs/<sha256>.pdf`, metadata terbaru per URL di `data/acquisition/records/<url-hash>.json`, riwayat pengambilan di `data/acquisition/observations/`, dan HTML asli di `data/acquisition/pages/`. Metadata mempertahankan tanggal/status sebagai nilai portal yang belum diverifikasi, bukan hasil penetapan keberlakuan hukum.

Toolchain workspace dipin ke Go 1.26.8, dengan minimum language version 1.26.0 karena dependency parser HTML resmi golang.org/x/net v0.59.0. Go yang mendukung automatic toolchain selection dapat mengambil toolchain yang diperlukan; alternatifnya pasang Go 1.26.8. Dependency dipin pada go.mod/go.sum. Python tidak dipakai untuk downloader produksi ini.

## Input batch dan discovery

File input berisi satu URL detail atau PDF per baris; baris kosong dan komentar `#` diabaikan. Untuk satu URL atau daftar URL kecil, gunakan `-url` berulang. Jangan memasukkan tanda kutip sebagai bagian isi baris file.

```powershell
go run ./src/server/cmd/cli collect -url "https://peraturan.bpk.go.id/Details/229798/uu-no-27-tahun-2022"
```

Untuk menghindari menyalin link dokumen satu per satu, collector dapat menemukan link detail dari halaman daftar BPK/Kemkomdigi dan mengikuti link `next` yang dikenali. Batas halaman/dokumen adalah batas akuisisi run, bukan klaim corpus lengkap atau pengubahan benchmark:

```powershell
go run ./src/server/cmd/cli collect -listing "https://peraturan.bpk.go.id/" -max-pages 3 -max-documents 20
go run ./src/server/cmd/cli collect -listing "https://jdih.komdigi.go.id/" -max-pages 3 -max-documents 20
```

Homepage yang tidak menyediakan link pagination mungkin hanya menghasilkan dokumen yang ditampilkan di halaman tersebut. Seed katalog berdasarkan filter/tahun dapat diberikan pada mode discover; API dinamis dan jaminan cakupan seluruh katalog belum tersedia. Jika layout tidak dikenali, collector melaporkan kegagalan; tidak menyatakan portal selesai dikumpulkan. Discovery bukan browser automation dan tidak mengeksekusi JavaScript.

## Resume, refresh, dan kegagalan

Run ulang perintah yang sama untuk melanjutkan batch. Record complete dipakai ulang hanya bila checksum PDF/HTML cocok dan versi parser metadata sesuai. Pada partial failure, PDF yang sudah valid dipakai ulang dan file yang gagal dicoba kembali. Perubahan versi parser mengulang ekstraksi metadata; file PDF yang valid tetap dapat digunakan. Gunakan `-refresh` untuk mengambil ulang metadata dan PDF dari portal ketika mengecek pembaruan:

```powershell
go run ./src/server/cmd/cli collect -input configs/sources.txt -refresh
```

Resume default bukan pemeriksaan update live: metadata baru di situs tidak diketahui bila record lokal digunakan ulang. Incremental production update berbasis dependency berada pada U01 dan belum aktif. Riwayat observation tetap disimpan ketika cursor record terbaru diganti.

Unduhan mengalir ke file sementara, dihitung SHA-256, dibatasi ukuran, dan diperiksa header `%PDF-` serta marker `%%EOF` sebelum dipromosikan. HTML halaman error dan unduhan yang tidak lengkap ditolak. Pemeriksaan ini mendeteksi kesalahan unduh umum, bukan validasi sintaks PDF lengkap, tanda tangan digital, atau isi hukum. Blob lokal dengan hash salah dikarantina sebelum diganti unduhan valid.

Receipt menyimpan source/final URL, waktu observasi, content type, ETag/Last-Modified bila tersedia, bytes, checksum, durasi, serta error per PDF. Metadata BPK/Kemkomdigi menyimpan judul, nomor, tahun, tipe, tanggal, sumber dan field yang ditemukan. Tautan putusan uji materi BPK dipisahkan dari PDF peraturan; `-include-related` mengikutkan unduhannya dengan tipe `related_judgment`.

Exit code: 0 seluruh input berhasil/valid reused; 1 ada unduhan/discovery gagal atau pembatalan; 2 argumen/input konfigurasi tidak valid. Progress ditulis ke stderr dan event/summary JSON Lines ke stdout. File partial atau HTML error tidak dihitung sebagai PDF berhasil.

## Batas akses dan resource

Default: dua worker dokumen, interval minimal satu detik per host, timeout HTTP 60 detik, total budget per dokumen tiga kali timeout, dan maksimum PDF 100 MiB. Semuanya konfigurasi collector, bukan angka benchmark required. Lihat `collect -help` untuk opsi. Tidak ada concurrency tak terbatas atau penurunan gate benchmark.

HTTP memakai connection reuse, maksimal tiga attempt untuk kegagalan transient, Retry-After bila tersedia, dan validasi redirect. Host yang diizinkan saat ini peraturan.bpk.go.id, jdih.komdigi.go.id, jdihn.go.id, dan www.jdihn.go.id melalui HTTPS tanpa credential di URL atau port khusus. Domain anggota/CDN lain tidak otomatis diizinkan; endpoint tersebut harus diperiksa sebelum connector diperluas. Tidak ada bypass login/CAPTCHA; halaman demikian gagal sebagai unduhan PDF.

JDIHN dapat menyumbang tautan detail /doc/ melalui discovery homepage; pemetaan metadata detail, search dinamis, dan PDF anggota belum diverifikasi melalui unduhan nyata. Jangan mengklaim scraper tiga portal lengkap. Periksa mekanisme akses dan ketentuan tiap portal sebelum menjalankan crawl besar menurut [rencana corpus](corpus-plan.md).

## Verifikasi awal

Pada smoke acquisition lokal 2026-09-19, dua seed menghasilkan PDF nyata: UU Nomor 27 Tahun 2022 dari BPK sebanyak 2.977.828 bytes/50 halaman, dan Permen Kominfo Nomor 20 Tahun 2016 dari Kemkomdigi sebanyak 487.961 bytes/24 halaman. Checksum dicocokkan terhadap file lokal dan kedua file dapat dibuka dengan pypdf sebagai pemeriksaan tambahan; pypdf bukan dependency runtime Go. Ukuran/halaman adalah observasi artefak saat itu, bukan janji bahwa portal tidak akan mengganti file.

Tests offline mencakup metadata/layout, dedup preview/download, pemisahan putusan, penolakan HTML/oversize/truncated body, partial retry, checksum resume, repair blob korup, URL/redirect policy, discovery bounds, cancellation, dan exit code CLI. Smoke download tidak membuktikan kelengkapan katalog atau kelulusan target latency/accuracy Hybrid GraphRAG.

## Hasil penyiapan antrean 2026-09-20

Run dibatasi 200 request halaman: 199 halaman berhasil disimpan dan satu seed BPK `Search?jenis=15` tidak menghasilkan tautan yang dikenali; error tetap tercatat, sehingga proses keluar dengan kode 1 (coverage parsial). Antrean berisi 3,160 URL detail unik, dengan distribusi jdih.komdigi.go.id: 435, peraturan.bpk.go.id: 2,715, jdihn.go.id: 10. Ini jumlah URL yang ditemukan, bukan jumlah PDF tersedia atau peraturan kanonis unik. Hasil mesin dan pekerjaan kelanjutan berada di `data/acquisition/handoff.json`.

Tiga PDF lama berjumlah 11.975.669 bytes, seluruh checksum cocok dan dapat dibaca pypdf (50, 24, serta 19 halaman). Tidak ada PDF tambahan diunduh pada sesi discovery ini. Semua pengujian Go serta go vet lulus; benchmark retrieval/model belum dijalankan.
