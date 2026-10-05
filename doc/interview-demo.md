# Demo lokal untuk interview

Dokumen ini menjelaskan demo RAG yang dapat dijalankan dari PDF hasil akuisisi,
beserta batasnya agar demonstrasi tidak disamakan dengan release Hybrid GraphRAG.
Prioritas demo mengikuti permintaan pengguna pada 2026-10-05 untuk interview pukul 14.00.

## Membuka dan menjalankan

Buka **http://127.0.0.1:8096** jika proses masih berjalan. Dari root repositori:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/start_demo.ps1
```

Script memakai Go dan Python yang terpasang, serta Ollama yang sudah berjalan
dengan `qwen2.5:7b`. Ia menyiapkan sample sekali, membangun executable Go, dan
membuat profil model terpisah `regulagraph-demo:latest` dengan konteks 8192.
Bobot model yang sudah ada dipakai kembali. Tidak ada API key, database, atau
deployment yang diperlukan. Proses dengan mode yang sama dipakai lagi tanpa
reload kode; setelah mengubah kode, hentikan proses lama sebelum menjalankan ulang.
Jika mode model/SearchOnly berbeda, script meminta proses lama dihentikan atau
checkbox UI digunakan, bukan diam-diam mengganti konfigurasi server.
Pada startup baru terminal tetap terbuka; Ctrl+C menghentikan server.

Jika proses awal dijalankan agent di background, PID tersimpan dalam
`artifacts/verification/20261005-interview-demo/server.pid`. Untuk menghentikannya,
periksa bahwa PID tersebut masih milik executable `.cache/regulagraph-demo.exe`
di repositori ini sebelum memakai `Stop-Process -Id <PID>`; PID dapat dipakai ulang
Windows setelah proses keluar.

Pencarian tanpa model:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/start_demo.ps1 -SearchOnly
```

Checkbox di UI juga memungkinkan pencarian saja tanpa menghentikan server.
Model dapat memerlukan waktu memuat bobot setelah lama tidak digunakan; coba satu
pertanyaan beberapa menit sebelum interview. Jangan menjalankan server dua kali
pada port yang sama. Gunakan URL `127.0.0.1`, sesuai pembatasan Host lokal.

## Alur yang sudah berjalan

Offline preparation memilih PDF deterministik dari receipt D01, memeriksa SHA-256
dan ukuran, lalu mengekspor text layer per halaman melalui PyMuPDF. Manifest
`SHA256SUMS` mengikat teks dan salinan record asli. Model tidak dipanggil saat tahap
ini. PDF asli tidak diubah. Sample default 24 dokumen; batas halaman/ukuran
mengecualikan sebagian dokumen, dan daftar skip tersimpan di `report.json`.

Go membaca export sekali, memverifikasi hash dan batas byte, membuat window teks
dengan overlap beserta offset UTF-8/halaman, lalu membangun indeks BM25 dalam memori.
Analyzer query memakai implementasi lexical yang sama dengan komponen produksi.
Lima potongan dari halaman berbeda diberikan ke model lokal melalui adapter
structured output. Klaim hanya diterima bila ID kutipannya berasal dari hasil
pencarian. UI menampilkan teks, halaman, URL portal, serta PDF lokal yang hash-nya
diperiksa kembali saat dibuka. Pertanyaan kosong, origin asing, input berlebih,
referensi model asing, dan korupsi artefak ditolak.

Python hanya digunakan untuk persiapan sample offline; retrieval, workflow, HTTP
dan validasi jawaban dijalankan Go. Demo ini belum memakai Rust/C++/Qdrant/Neo4j
dalam jalur pertanyaannya. Komponen produksi tersebut tetap ada dan tidak diganti.

## Urutan demonstrasi

1. Tanyakan: **Kapan pengendali wajib memberitahukan kegagalan pelindungan data pribadi?**
2. Tunjukkan kutipan hasil pencarian yang muncul lebih dahulu, lalu jawaban model.
3. Klik **S1** dan **Buka PDF**; pada sample ini sumbernya UU 27/2022 halaman 19,
   Pasal 46. Periksa kalimat sumber bersama jawaban, jangan hanya percaya label.
4. Coba **Apa saja hak subjek data pribadi?**, lalu bandingkan klaim dengan kutipan.
   Jawaban dapat hanya mencakup hak yang ada di potongan yang ditemukan.
5. Nonaktifkan model untuk memperlihatkan retrieval dan sumber secara terpisah.

Penjelasan yang akurat: “Saya membangun arsitektur GraphRAG regulasi dengan runtime
Go, Rust, dan C++. Demo ini menjalankan baseline RAG BM25 dan model lokal pada PDF
nyata, lengkap dengan sumber halaman. Integrasi graph, dense retrieval, versioning
hukum, dan benchmark end-to-end masih dilanjutkan.”

## Batas yang tetap berlaku

Ini bukan corpus produksi terpublikasi: tidak ada resolusi canonical/versi hukum,
filter berlaku-pada-tanggal, dense embedding, graph traversal, reranker, atau
acceptance benchmark pada jalur demo. Chunk demo berupa window halaman, bukan
hasil structural chunking produksi. PDF tanpa text layer tidak di-OCR. Hash
manifest mendeteksi perubahan setelah export; ia bukan tanda tangan keaslian atau
bukti bahwa parser selalu benar. File lokal diasumsikan disiapkan operator tepercaya.

Jawaban berstatus `unreviewed_draft`; validitas ID kutipan tidak membuktikan seluruh
makna klaim didukung. Hasil kosong menghasilkan `no_evidence`, model boleh abstain,
dan kegagalan generation tetap menampilkan evidence dengan status berbeda.
Satu generation diperbolehkan sekaligus agar laptop tidak kelebihan beban. Tidak
ada exact tokenizer/prompt admission produksi pada preview ini. Target required
di `configs/benchmark-targets.yaml` tidak berubah dan tetap NOT_MEASURED.

CLI `demo -endpoint` menerima provider HTTPS yang dikonfigurasi eksplisit; script
default selalu memakai Ollama loopback. Tidak ada pengiriman corpus ke provider
remote pada uji yang dilaporkan di [laporan verifikasi](verification-report-interview-demo.md).
