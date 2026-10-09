# Checkpoint percobaan profil EXTRACT lokal

Dokumen ini menyimpan hasil diagnostik dan langkah resume agar pekerjaan dapat
dilanjutkan setelah batas usage sesi. Baseline kode `ff54de95771992a9a4246010c04f9b318ccded92`,
tanggal 2026-10-09, branch `feat/real-pdf-graphrag`. Ini bukan acceptance release.

## Status dan toleransi kualitas

Pengguna mengizinkan kualitas Qwen sementara untuk eksperimen/draft. Akurasi dan
kelengkapan boleh tetap UNREVIEWED/NOT_MEASURED; JSON rusak, referensi entitas
hilang, atau provenance invalid tidak diterima sebagai batch sukses. Model yang
lebih sesuai berpotensi memperbaiki hasil, tetapi peningkatan belum terbukti.
Target `configs/benchmark-targets.yaml` tidak diubah.

**Belum ada EXTRACT valid untuk seluruh PDF**, sehingga RESOLVE, ASSEMBLE,
publication graph dan jawaban dari graph PDF ini belum dijalankan. Tidak ada
hasil diagnostik yang dimasukkan ke corpus. Corpus hybrid lama tetap tersedia.

## Input dan lingkungan

Semua tujuh percobaan memakai seluruh chunk pembuka PP 12/2006 yang sama,
354 unit sumber; tidak memangkas teks untuk meluluskan percobaan. PDF SHA-256:
`c2c761194c0a15d1858c38c7c45308e045539a80be5fd67b23cd4a050f7dbbfb`.
Ini satu chunk dari corpus 35 chunk, bukan evaluasi seluruh PDF.

Ollama 0.32.1 pada Windows, GPU RTX 3060 Laptop 6144 MiB. Qwen2.5 kelas 7B
dan Qwen3-4B-Instruct-2507 memakai Q4_K_M, offload GPU/CPU campuran. Qwen3
metadata menyatakan 4,022,468,096 parameter; identitas blob metadata
`85e4a5b7b8ef0e48af0e8658f5aaab9c2324c76c1641493f4d1e25fce54b18b9`
bukan attestation hash ulang pada seluruh invocation.

Qwen2.5 memakai context 8192. Qwen3 alias v1 memakai 16384; v2 memakai 12288,
repeat_penalty 1.1, repeat_last_n 512. Semua output cap 4096. Temperature 0,
kecuali sampled-schema 0.7/top_p 0.8/seed 42. Prompt, schema, format dan
resource residency berbeda; waktu di bawah bukan perbandingan performa terkontrol.

## Hasil aktual

Root artefak lokal: `artifacts/operational/pp12-graphrag-local-v1/`.
Setiap direktori memuat actual-request, actual-response, source dan diagnostic-result.
Artefak diabaikan Git; tidak tersedia otomatis pada clone baru.

| Direktori | Model/profil | Detik | Token output | Hasil |
| --- | --- | ---: | ---: | --- |
| `qwen25-gpu` | Qwen2.5, schema v3 dengan enum ontology | 245.61 | 4096 | length; JSON terpotong |
| root | Qwen3 v1, schema qualifier bertipe | 266.03 | 4096 | length; pengulangan assertion |
| `explicit-schema` | Qwen3 v1, schema dan contoh format di prompt | 333.92 | 4096 | length; pengulangan mention |
| `json-object` | Qwen3 v2, JSON-object format | 110.42 | 3897 | stop tetapi prosa tambahan setelah JSON; ditolak |
| `sampled-schema` | Qwen3 v2, sampling 0.7 | 103.92 | 4096 | length; JSON terpotong |
| `entity-first` | Qwen3 v2, shape diagnostik entities/relations | 49.78 | 2194 | JSON utuh, 20 entitas/10 relasi; ada relasi tanpa dukungan teks |
| `entity-first-context` | Shape diagnostik, satu system message dan sumber langsung | 70.50 | 3004 | JSON utuh, 23 entitas/12 relasi; referensi qualifier `a`/`b` tidak dideklarasikan, effective_on memakai literal |

Script `run_qwen25.py`, `run_diagnostic.py`, `run_explicit_schema.py`,
`run_json_object.py`, `run_sampled_schema.py` keluar 1 karena JSONDecodeError.
`run_entity_first.py` dan `run_entity_first_context.py` keluar 0 untuk parsing JSON,
**bukan** kelulusan C01/ontology, kualitas atau production projection.
Schema eksperimental tidak dipromosikan; produksi v1/v2/v3 tetap tidak berubah.

Contoh masalah yang terlihat: entity-first melabeli rentang “PRESIDEN REPUBLIK
INDONESIA” sebagai place dan mengeluarkan permits/prohibits dari rentang
“PENJUALAN ATAS BARANG MEWAH DENGAN RAHMAT”. Exact slicing saja tidak membuktikan
hubungan benar. Tidak ada angka precision/recall yang dihitung dari inspeksi ini.

Canary `probe_provider.py` dan `probe_provider_conflict.py` keluar 0. Schema
satu nilai konstan dipatuhi pada OpenAI-compatible dan native Ollama, termasuk
ketika prompt meminta puisi tanpa JSON. Ini membuktikan enforcement pada canary
tersebut, bukan keandalan seluruh schema kompleks. Raw hasil ada di
`provider-probe/` dan `provider-probe-conflict/`.

Review independen agent `verify_index_abort` memeriksa envelope, template, sumber
dan kegagalan awal; tidak menemukan bukti bahwa adapter menghilangkan ontology
atau sumber. Review tidak memberi approval terhadap hasil EXTRACT atau kualitas.

## Pemulihan dan titik resume

Model EXTRACT eksperimen sudah di-unload. BGE pada 55072 dan generator jawaban
CPU pada 55110 sudah dinyalakan kembali. Percobaan restore BGE pertama gagal
karena DLL CUDA tidak ditemukan; restore berhasil setelah folder
`C:/Users/ASUS/AppData/Roaming/Python/Python312/site-packages/torch/lib`
ditambahkan ke PATH proses. API lama pada 58097 sempat gagal readiness, lalu
berhasil setelah readmission binary/config semula; penyebab awal belum diisolasi.

`restoration.json`, `restored-api-ready.json` dan log restore merekam keadaan akhir:
health generator OK, API `ready` dengan capability `evidence_and_answer_draft`.
CLI hybrid evidence pada corpus lama keluar 0 dan menghasilkan
COMPLETION_STATUS_SUCCEEDED/COMPLETENESS_PARTIAL; hasil di `restored-evidence.json`.
Tidak melakukan ulang generation jawaban pada checkpoint ini. PID terbaru ada
di metadata proses `artifacts/operational/pp12-v1/`; periksa sebelum stop/start.

Schema DB baru `regulagraph_ops_pp12_graph_v1` sudah dimigrasikan sampai 0027,
tetapi belum berisi job EXTRACT sukses. `environment.ps1` pada root eksperimen
dan binaries CLI/gateway/coordinator tersedia. Konfigurasi masih **persiapan**:
model/producer belum dibekukan dan schema eksperimen jangan dipakai produksi.

Urutan resume:

1. Uji desain ekstraksi bertahap atau profil lokal yang lebih sesuai pada sumber
   gagal yang sama. Simpan semua attempt. Jangan melanjutkan profil terakhir
   seolah-olah sudah lolos; jangan menghapus qualifier/fakta invalid diam-diam.
2. Setelah output sumber/ontology valid, bekukan model/prompt/schema/config baru,
   ekspor producer Go/Rust yang sama dan jalankan seluruh 35 chunk dengan replay.
   Perubahan format perlu versi schema dan compatibility review, bukan overwrite.
3. Siapkan keputusan identitas berdasarkan hasil aktual. Regulasi/pasal di luar
   inventory memerlukan canonical yang sah; provisional non-BIND bukan pengganti.
   Jika review manusia diperlukan, sajikan packet konkret kepada pengguna.
4. RESOLVE → ASSEMBLE → publish graph → query/jawaban bersitasi, simpan run asli.
   Corpus hybrid lama tidak perlu dibangun ulang untuk sekadar mencoba aplikasinya;
   ikuti [handoff operasional](operational-handoff.md).

Status akhir: canary dan restoration smoke PASS; EXTRACT penuh dan jalur graph
nyata BLOCKED oleh output/integrasi model; kualitas dan required benchmark
NOT_MEASURED. Tidak ada API hosted dipanggil atau deployment dilakukan.
