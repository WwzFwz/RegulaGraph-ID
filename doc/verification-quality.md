# Verifikasi kualitas, latency, throughput, dan resource

Dokumen ini mengarahkan agent verifikasi untuk memeriksa hasil pengukuran secara bersamaan, sesuai prioritas pengguna pada accuracy dan kecepatan. Semua angka required tetap bersumber tunggal pada [benchmark-targets.yaml](../configs/benchmark-targets.yaml), dengan aturan pada [benchmark-policy](benchmark-policy.md). Dokumen ini tidak menambah atau menurunkan ambang.

## Kelayakan run

Sebelum run dinilai, bekukan corpus snapshot, split/group dataset, software/config/model/tokenizer/prompt, target-suite hash, hardware/driver, resource limits, workload, cache policy, warmup, dan seed bila relevan. Hardware referensi dalam YAML adalah asumsi yang dinyatakan; jangan melaporkan hasil laptop sebagai hasil profil referensi tanpa kesetaraan kondisi. Prasyarat tidak lengkap berarti BLOCKED/NOT_MEASURED untuk gate tersebut.

Catat arrivals yang dijadwalkan, diterima, ditolak, timeout, cancelled, partial, dan gagal. Jangan menghilangkan request lambat atau rejected arrivals. Pisahkan service time, queue time, dan end-to-end latency; p95 tahap tidak dijumlahkan menjadi p95 end-to-end. TTFT memakai token jawaban substantif, bukan heartbeat/metadata. Load generator tidak boleh menutupi antrean dengan hanya mengirim request baru setelah request lama selesai bila workload mensyaratkan open-loop.

## Kualitas dan performa harus dinilai bersama

| Area | Yang diperiksa reviewer |
| --- | --- |
| Parsing/chunk | Reading order, batas pasal/ayat, CER/WER bila berlaku, nomor/negasi/exception, sumber/offset, detik per halaman, peak RSS |
| Extraction/resolution | Precision/recall per tipe, false merge/split, candidate coverage, dukungan sumber, biaya/model calls, throughput |
| Retrieval | Recall/ranking/evidence sufficiency, factual/multi-hop/temporal/typo/informal/code-switch slices, latency backend/fusion/rerank |
| Answer/citation | Claims didukung, citation precision/coverage, versi tepat, abstention/conflict, TTFT/completion, tokens/cost |
| Inference | Reference parity, model/tokenizer/precision cocok, truncation, cold/warm, batch shape, queue, VRAM/RSS |
| Update/storage | Logical equivalence, idempotency, dependency closure, publication readiness, freshness lag, throughput commit |
| Isolation | Query dan ingestion bersamaan, absolute gates dan degradasi, kapasitas ingestion tidak dimatikan agar query tampak cepat |

Gold label harus ditinjau sesuai kebijakan dataset. LLM judge dapat membantu, tetapi bukan bukti tunggal untuk gate yang mensyaratkan reviewer manusia. Parafrasa dari base question yang sama tidak boleh tersebar di split berbeda. Kasus temporal/exception sulit tetap masuk workload yang disepakati.

Empat baseline harus menyatakan perbedaan anggaran konteks, reranking, model, cache dan routing; jangan menyebut perbandingan adil bila variabel tersebut berubah tanpa pelaporan. Optimasi pruning atau pengurangan kandidat harus diukur dampaknya terhadap evidence sufficiency; tidak diasumsikan aman karena latency lebih rendah.

## Menilai gate dan menangani kegagalan

E01 harus membuktikan perilaku evaluator pada denominator nol, sampel kurang, NaN/infinity, missing field, unit salah, manifest mismatch, rejected arrival, timeout, dan threshold boundary. PASS tidak boleh berasal dari empty dataset atau fungsi placeholder. Angka di YAML saja belum berarti enforcement aktif.

Jika run valid gagal, simpan raw results, profil bottleneck, perbaiki implementasi, dan uji ulang. Jangan mengganti workload, denominator, target atau kriteria lulus diam-diam. Perubahan benchmark hanya dapat diusulkan dengan target vs hasil, kondisi run, bottleneck, optimasi yang dicoba, alternatif, serta dampaknya; persetujuan pengguna diperlukan sebelum standar berubah. Selama belum disetujui, angka lama berlaku.

Laporan akhir membedakan invariant PASS, benchmark NOT_MEASURED, dan model quality yang belum dinilai. Keberhasilan build C01, fixture kompatibilitas, atau batch download bukan bukti kelulusan Hybrid GraphRAG end-to-end.
