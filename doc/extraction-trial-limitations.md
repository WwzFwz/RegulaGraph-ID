# Kondisi percobaan EXTRACT dan batas kesimpulan

Dokumen ini merangkum kondisi eksperimen EXTRACT yang dilaporkan pada 2026-10-09,
agar hasil satu setup model tidak dibaca sebagai penilaian umum atas kemampuan
GraphRAG atau seluruh implementasi RegulaGraph-ID. Ini ringkasan bukti engineering
dan keterbatasan eksperimen, bukan hasil perbandingan model atau acceptance release.

## Kesimpulan yang didukung bukti

RegulaGraph-ID sudah memiliki implementasi pemrosesan dokumen, registry berbukti,
indeks dense/BM25, graph assembly/publication, retrieval dan jawaban bersitasi.
Beberapa boundary telah diuji dengan backend/native runtime nyata dan fixture;
PDF PP 12/2006 sudah menjadi 35 chunk, terindeks dan menghasilkan draft jawaban
bersitasi melalui CLI/API. [Bukti indeks](verification-report-real-index.md),
[jawaban nyata](verification-report-real-answer.md), dan
[transaksi registry](verification-report-provisional-alias.md) menjelaskan cakupan
dan batas masing-masing hasil tersebut.

**Hambatan pada percobaan EXTRACT yang dilaporkan adalah output yang tidak memenuhi
validasi sumber, pengulangan/truncation, serta timeout pada konfigurasi model lokal
yang diuji.** Eksperimen ini memakai keluarga Qwen2.5 7B terkuantisasi; belum ada
perbandingan EXTRACT yang terkontrol terhadap model hosted yang lebih kuat atau
model lokal berparameter lebih besar. Hasil tersebut tidak menetapkan batas
kemampuan arsitektur maupun membuktikan bahwa model adalah satu-satunya penyebab.
Prompt, representasi input, budget output, runtime dan kapasitas hardware juga
berbeda pada beberapa percobaan. Fitur yang belum diimplementasikan serta evaluasi
gold/performa tetap dicatat pada [rencana pengembangan](development-plan.md).

## Setup yang benar-benar dicoba

| Aspek | Kondisi yang tercatat | Batas interpretasi |
| --- | --- | --- |
| Model EXTRACT | Qwen2.5 kelas 7B; metadata Ollama menyatakan **7.6B**, GGUF **Q4_K_M**, alias `regulagraph-demo:latest` | Alias bukan bukti weights immutable; beberapa diagnostic Ollama tidak meng-attest ulang bobot |
| Identitas GGUF llama.cpp | SHA-256 `2bada8a7450677000f678be90653b85d364de7db25eb5ea54136ada5f3933730` | Identitas ini berlaku untuk GGUF yang dicatat/di-hash pada run terkait, bukan semua model bernama Qwen |
| llama.cpp | Build `b11515-3d65c90d0`, **CPU 4 thread**, 1 slot, context 8192, GPU layers **0**, context shift nonaktif | Timeout pada konfigurasi ini bukan pengukuran performa GPU atau model yang lebih besar |
| Host CPU | Manifest mencatat `AMD64 Family 25 Model 68 Stepping 1, AuthenticAMD` | Tidak menebak nama produk CPU, RAM, atau GPU yang tidak dibekukan dalam manifest tersebut |
| Ollama | Context 8192; satu snapshot resident melaporkan `size_vram=4254764891` byte, sekitar **4,25 GB** | Sebagian eksperimen memakai alokasi GPU; tidak tepat menyebut semua run CPU-only. Angka resident bukan peak VRAM atau bukti seluruh layer berjalan di GPU |
| Input diagnostic | Chunk pembuka PP 12/2006 yang sama; corpus hasil CHUNK berjumlah 35 chunk | Percobaan ini belum merupakan evaluasi lengkap seluruh PDF atau dataset lintas regulasi |
| Format/anggaran | v2 kutipan exact dan v3 unit sumber; cap umumnya 4096, satu eksperimen feedback 2048; temperature request 0 pada run terkait | Perubahan prompt/format/cap/runtime/deadline membuat perbandingan latency antar-run tidak terkontrol |
| Provider pembanding | Tidak ada panggilan model hosted/cloud pada rangkaian diagnostic ini | Belum ada bukti model hosted tertentu lebih akurat atau lebih cepat untuk tugas ini |
| Ground truth | Belum ada evaluasi gold precision/recall untuk rangkaian ini | Validasi locator/schema bukan pengukuran akurasi semantik atau kelengkapan relasi |

Sumber kondisi: [diagnostik runtime](verification-report-extraction-runtime.md),
[uji v2](verification-report-quote-extraction.md),
[feedback](verification-report-extraction-feedback.md), dan
[uji v3](verification-report-indexed-extraction.md). Artefak lokal yang mendasari:
`artifacts/verification/20261009-llama-extraction/manifest.json`,
`copy-examples/ollama-models.json`, serta
`artifacts/verification/20261009-indexed-extraction/props.json` dan `result.json`.
Artefak tersebut diabaikan Git; laporan merangkum informasi yang boleh dibaca publik.

## Hasil aktual, terpisah dari dugaan penyebab

v2 pada Ollama sempat menghasilkan respons lengkap, tetapi empat dari sepuluh
locator tidak cocok dengan sumber. Prompt lanjutan menghasilkan pengulangan atau
mention yang tidak muncul persis dalam teks. Validator menolak hasil tersebut;
penolakan menunjukkan guard sumber bekerja pada kasus itu, bukan bahwa accuracy
sistem telah diukur. Run llama.cpp v2 CPU timeout sekitar 300 detik; diagnostic
v3 CPU timeout sekitar 600 detik tanpa completion lengkap. Run timeout tidak
memberikan output untuk menilai precision/recall. Detail tiap attempt tetap ada
di laporan sumber, termasuk kegagalan dan perbedaan konfigurasi.

## Hipotesis pengembangan, belum hasil eksperimen

Model dengan kemampuan extraction/instruction-following yang lebih sesuai
**berpotensi** memperbaiki kepatuhan schema, pemilihan span dan hubungan berbukti.
Kandidat berikut dapat berupa model hosted atau model lokal yang lebih besar pada
hardware memadai. Lokasi hosted, jumlah parameter lebih besar, atau harga lebih
tinggi sendiri bukan bukti kualitas yang lebih baik pada regulasi Indonesia.

GPU terutama merupakan opsi untuk meningkatkan kapasitas dan kecepatan inference,
termasuk memungkinkan percobaan model lebih besar. Memindahkan bobot yang sama ke
GPU tidak dengan sendirinya membuktikan kenaikan accuracy. Efek pilihan model,
quantization, prompt, format sumber dan runtime perlu dinilai terpisah; tidak ada
kenaikan accuracy, latency, atau throughput yang diklaim dari hipotesis ini.

Model dapat diganti melalui [profil dan pin](semantic-model-profiles.md) tanpa
menulis ulang pipeline. Jalankan sumber gagal yang sama lebih dahulu, kemudian
seluruh PDF dan sampel regulasi representatif. Catat exact model/version/weights,
precision, template/prompt/schema, context/output budget, hardware/offload, waktu,
biaya, validitas sumber, mentions/relations yang hilang/salah, serta semua attempt
gagal. Perbandingan kualitas memerlukan label yang ditinjau; JSON valid atau
respons lebih cepat saja belum menutup acceptance. Target benchmark tetap sama.

## Paragraf ringkasan untuk laporan

> Implementasi RegulaGraph-ID telah menunjukkan sejumlah integrasi engineering
> yang berfungsi, termasuk pemrosesan PDF, indeks hybrid, registry berbukti dan
> draft jawaban bersitasi. Ekstraksi graph dari corpus nyata masih terhambat pada
> percobaan Qwen2.5 7B terkuantisasi dan konfigurasi inference lokal yang terbatas.
> Hasil tersebut bersifat spesifik terhadap setup eksperimen; model hosted yang
> lebih kuat atau model lokal lebih besar dengan dukungan GPU belum dibandingkan.
> Penggantian model merupakan jalur perbaikan yang direncanakan, dengan kualitas
> dan performa tetap harus dibuktikan melalui evaluasi yang sama. Keterbatasan
> eksperimen model dibedakan dari pekerjaan implementasi dan acceptance yang
> masih terbuka.
