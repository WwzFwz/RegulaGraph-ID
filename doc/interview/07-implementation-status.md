# Status implementasi dan bukti aktual

Dokumen ini memisahkan keadaan repository yang diperiksa pada 5 Oktober 2026 dari
asumsi sistem lengkap pada dokumen 01–06, 08, dan 09. Dokumen utama menjelaskan cara kerja
arsitektur ketika semua bagian sudah terintegrasi; dokumen ini menjawab apa yang
benar-benar dapat dijalankan dan dibuktikan sekarang.

## Arti status

| Status | Arti |
| --- | --- |
| DEMO | Dipakai jalur localhost yang diuji dengan PDF dan model nyata |
| KOMPONEN | Implementasi/library atau integrasi terbatas tersedia; bukan kelulusan pipeline penuh |
| RENCANA | Fungsi belum tersedia lengkap/tersambung, termasuk scaffold |

## Keadaan komponen

| Bagian | Keadaan aktual |
| --- | --- |
| Acquisition | Download, receipt, discovery dan audit tersedia; seluruh coverage corpus belum lengkap |
| Dokumen | Pipeline durable sampai EXTRACT tersedia; OCR/tabel dan gold parsing belum lengkap |
| Resolution | Candidate/exact binding, model proposal dan registry writer tersedia; review/resume serta seluruh canonical lifecycle belum selesai |
| Graph | Library materialisasi endpoint tersedia; Neo4j store dan query traversal masih scaffold |
| Native inference | Embedding BGE-M3 dan cross-encoder ONNX C++ tersedia dengan pengujian terbatas; bukan generator demo |
| Indexing | Worker INDEX/library dan writer Qdrant awal tersedia; coordinator corpus nyata end-to-end belum lengkap |
| Query classifier | `retrieval/query/classifier.go` masih scaffold; routing otomatis belum aktif |
| Entity linker query | `retrieval/query/entity_linker.go` masih scaffold |
| Retrieval | Vector/Hybrid dense+BM25 dan RRF tersedia sebagai komponen; profil graph belum diterima CandidateSearch |
| Evidence | Hidrasi direct source dan packing tersedia; parent/exception/path completeness belum lengkap |
| Answering | Draft non-streaming, citation/structure validation tersedia; streaming dan semantic acceptance belum lengkap |
| Adaptive loop | Pencarian ulang berbasis evidence gap belum otomatis berjalan dalam workflow produksi |
| Evaluasi | Harness/metrics tersedia; gold manusia lengkap, empat baseline dan acceptance required belum selesai |
| Deployment | Tidak dilakukan pada pekerjaan ini |

Ada pula temuan integrasi terbuka: `indexing/initial_prepare.go` dan
`retrieval/hydration.go` perlu memperbaiki asumsi ID artefak fisik sama dengan ID
record logis sebelum hasil Rust content-addressed dipakai sepenuhnya.

## Jalur demo nyata

```text
PDF D01 -> Python offline / PyMuPDF -> page text + SHA256SUMS
                                          |
                                    Go verify + BM25 index
                                          |
Browser -> Go HTTP -> BM25 -> 5 kutipan -> Ollama / Qwen lokal
                               |                 |
                        halaman/PDF       validasi source IDs
                               +------ tampilan draft ------+
```

Demo memuat sample 24 dokumen, 1.061 halaman, 2.071 passage. Ia tidak memanggil
Qdrant, Neo4j, embedding C++, structural chunker Rust atau adaptive retry. Window
demo memakai maksimal 1.600 rune dan overlap sampai 160 rune; ini bukan kebijakan
chunk produksi. Citation-nya menunjuk halaman dan byte excerpt, bukan penetapan
versi pasal yang telah diverifikasi secara hukum.

UI mengirim pencarian evidence terlebih dahulu. Bila generation diaktifkan, UI
mengirim request kedua; retrieval diulang sebelum satu slot model menghasilkan
draft terstruktur. Unknown/duplicate source IDs ditolak. Status yang ditampilkan:
`evidence_only`, `no_evidence`, `abstain`, `generation_failed`, atau
`unreviewed_draft`. Klaim dengan ID valid masih dapat salah secara semantik.

Pada smoke, pencarian berlangsung sekitar 0,5–1 ms; generation beberapa detik
sampai belasan detik menurut keadaan model. Ini observasi sample, bukan p95/SLA.
Pertanyaan umum pernah menghasilkan pembalikan pelaku hukum; prompt diperketat
dan kasus dicatat. Itu tidak menghasilkan klaim semantic quality PASS.

Cara menjalankan dan mematikan: [panduan demo](../interview-demo.md).
Bukti tes/model/browser: [laporan demo](../verification-report-interview-demo.md).
Detail native: [native-inference](../native-inference.md).
Pekerjaan yang tersisa: [development-plan](../development-plan.md).

## Menggunakan bahan interview

Untuk pertanyaan desain, gunakan dokumen 01–06, 08, dan 09 dengan framing “dalam arsitektur
lengkap, sistem bekerja seperti ini”. Untuk pertanyaan hasil implementasi, metrik,
pengalaman melabeli gold atau deployment, gunakan status dan bukti di dokumen ini.
Asumsi desain lengkap tidak mengubah hasil test yang belum dijalankan menjadi PASS,
tidak menambah angka benchmark, dan tidak mengubah sejarah implementasi.

## Peta file demo

| Urutan | File | Isi yang dapat dijelaskan |
| --- | --- | --- |
| 1 | [scripts/start_demo.ps1](../../scripts/start_demo.ps1) | Setup/build lokal, profil Ollama, startup/reuse; mode berbeda tidak diam-diam dipakai |
| 2 | [tooling/corpus/prepare_demo.py](../../tooling/corpus/prepare_demo.py) | Python offline: pilih PDF, periksa receipt/hash, ekspor text layer per halaman dan SHA256SUMS |
| 3 | [cmd/cli/demo.go](../../src/server/cmd/cli/demo.go) | Composition root Go: muat data, index, generator, handler, listener dan shutdown |
| 4 | [adapters/storage/preview.go](../../src/server/internal/adapters/storage/preview.go) | Verifikasi manifest, path confinement, batas file/bytes dan metadata sumber |
| 5 | [retrieval/preview.go](../../src/server/internal/retrieval/preview.go) | Passage window, postings, TF/IDF, BM25, ranking dan seleksi halaman |
| 6 | [workflows/preview.go](../../src/server/internal/workflows/preview.go) | Retrieval → opsional generation; satu slot model, status gagal/abstain |
| 7 | [answering/preview.go](../../src/server/internal/answering/preview.go) | Prompt, schema output, batas klaim dan pengecekan source IDs |
| 8 | [adapters/inference/llm.go](../../src/server/internal/adapters/inference/llm.go) | HTTP structured-output adapter; timeout, batas respons, model/status/accounting checks |
| 9 | [api/preview.go](../../src/server/internal/api/preview.go) | HTTP input/origin/host/deadline dan endpoint PDF terverifikasi |
| 10 | [api/preview.html](../../src/server/internal/api/preview.html) | UI, POST evidence lebih dahulu, lalu generation, render teks dan citation |
| 11 | [domain/local_preview.go](../../src/server/internal/domain/local_preview.go) | Nilai internal/UI halaman, passage, klaim dan jawaban; bukan kontrak worker baru |
| 12 | [configs/demo.Modelfile](../../configs/demo.Modelfile) | Profil Qwen lokal khusus demo; bukan pilihan model release yang sudah tervalidasi |

Semuanya **DEMO**. Lokasi library produksi pada [peta kode](05-code-map.md) tetap
penting untuk menjelaskan arsitektur, tetapi tidak semuanya dipanggil tombol demo.

