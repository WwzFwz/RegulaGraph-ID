# Target numerik wajib untuk performa dan kualitas

Dokumen ini menjelaskan target ambisius yang ditetapkan atas arahan pengguna pada 2026-09-19. Perannya memberi angka penerimaan yang jelas sebelum implementasi diukur. Sumber angka yang otoritatif adalah [configs/benchmark-targets.yaml](../configs/benchmark-targets.yaml), suite regulagraph-performance-v1.

Status target: **REQUIRED_UNMEASURED**. Angka merupakan sasaran desain yang wajib dikejar pada profil referensi, bukan klaim benchmark, hasil penelitian, atau jaminan hardware pengguna. Pipeline dan runner masih scaffold; belum ada pemeriksaan otomatis gate saat request berjalan.

## Asumsi referensi

Server aplikasi/database memakai 16 core CPU fisik, RAM 64 GiB, NVMe lokal, dan satu GPU 24 GiB khusus embedding/reranker. Go, Rust, Qdrant, Neo4j, serta PostgreSQL berada pada node tersebut dengan resource limit tercatat. Sistem operasi referensi Linux x86-64; build Windows pengembangan tidak dianggap hasil benchmark server.

Generation memakai endpoint khusus terpisah yang model/provider-nya dibekukan sebelum pengujian, dengan RTT p95 maksimal 20 ms dan kapasitas untuk beban 2 request/detik. Semua waktu provider dan jaringan tetap masuk latency jawaban. Ini bukan asumsi bahwa satu GPU 24 GiB sekaligus melayani seluruh generation. CPU/GPU model persis, precision, tokenizer, prompt, runtime, dan konfigurasi harus dicatat sebelum hasil dinilai comparable.

Corpus minimal 10.000 dokumen, 100.000 chunk, 100.000 canonical entities, dan 1 juta relasi. Query multi-hop referensi mencakup rantai bukti 2–4 hop; itu bukan hard limit traversal produk. Corpus harus memiliki versi/perubahan dan sumber saling merujuk. Deployment lebih kecil/besar boleh diuji sebagai profil eksplorasi; target utama tidak otomatis turun atau dianggap lulus dari hardware yang berbeda.

## Beban dan pengukuran

Jalankan 3 run performa setelah warm-up 120 detik. Setiap run terukur minimal 20 menit. Retrieval lengkap tanpa generation menerima 10 request/detik dengan minimal 10.000 request per run; jawaban lengkap menerima 2 request/detik dengan minimal 2.000 request per run. Arrival bersifat open loop: beban tidak diperlambat saat server lambat. Batas 32 in-flight tidak membenarkan request yang dijadwalkan hilang dari denominator.

Result cache dan query-embedding cache dimatikan untuk penerimaan utama. Indeks, connection pool, dan model boleh warm. Query maksimal 128 token; reranker memproses 50 pasangan, masing-masing maksimal 512 token. Konteks generation total maksimal 8192 token dan jawaban lengkap maksimal 256 token. Dataset latency harus memiliki median output minimal 128 token dan p90 minimal 224 token, sehingga menjawab sangat pendek bukan jalan untuk lulus. Pertanyaan yang memang memerlukan jawaban lebih panjang masuk stress suite tersendiri.

Latensi dihitung dari arrival terjadwal yang terlihat load generator, termasuk antrean. Time-to-first-token dihitung pada token jawaban substantif; loading/heartbeat tidak dihitung. Untuk request tidak selesai, latency yang tidak tersedia dianggap tak hingga dalam evaluasi quantile dan kegagalannya masuk success ratio. Gunakan nearest-rank percentile; tampilkan juga timeouts, dropped arrivals, serta distribusi panjang input/output.

Seluruh required gate applicable harus lulus pada setiap run valid. Jangan merata-ratakan run gagal dengan run cepat. Ketidakpastian statistik tetap dilaporkan; estimasi point menjadi pembanding target pada v1, bukan bukti bahwa akurasi populasi persis sama dengan angka tersebut.

## Ringkasan target utama

| Sasaran | Target wajib |
| --- | --- |
| Evidence + reranking + konteks siap | p95 <=500 ms; p99 <=800 ms pada 10 RPS |
| Token jawaban pertama | p95 <=2 detik; p99 <=3 detik pada 2 RPS |
| Jawaban lengkap beserta citation | p95 <=10 detik; p99 <=15 detik untuk output <=256 token |
| Jeda token p95 | <=50 ms |
| Request berhasil diselesaikan | >=99,9% seluruh arrival terjadwal |
| Overhead Go eksklusif p95 | <=20 ms; peak RSS <=512 MiB |
| Embedding query / rerank 50 pasangan | p95 <=50 ms / <=250 ms |
| Recall@20 / nDCG@10 | >=97% / >=0,90 |
| Multi-hop dengan seluruh bukti tersedia | >=90% |
| Jawaban benar / minimum per slice | >=95% / >=90% |
| Citation precision / coverage | >=99% / >=98% |
| Pemilihan versi sesuai pertanyaan temporal | >=99% |
| PDF teks, parsing sampai chunk/source mapping | >=100 halaman/detik agregat 8 worker |
| Scan cetak standar 300 DPI sampai chunk/source mapping | >=2 halaman/detik |
| Normalisasi + chunking teks parsed di memori | >=10 MiB/detik |
| Graph assembly / commit Neo4j | >=50.000 / >=5.000 relasi per detik |
| Update satu dokumen <=10 halaman/50 chunk | p95 <=60 detik sampai snapshot terlihat |
| Update 1% corpus | <=10% waktu full rebuild, hasil logis setara |
| Query saat ingestion berjalan | p95 naik <=20%; batas absolut tetap berlaku |
| Throughput ingestion saat mixed load | >=50% ingestion-only; ingestion tidak boleh dihentikan |
| Integritas sumber, snapshot, replay, wire | 100% fixture/record memenuhi invariant; 0 orphan/mismatch |

Throughput parsing tidak mencakup embedding atau ekstraksi LLM. Graph assembly adalah transformasi memori; commit adalah waktu sampai database mengakui persistensi. Publication <=5 detik di YAML dimulai setelah seluruh artefak siap; end-to-end update <=60 detik mencakup langkah sebelumnya. Definisi ini mencegah angka CPU-only dipresentasikan sebagai kecepatan keseluruhan pipeline.

## Kualitas sebagai syarat performa

Gunakan minimal 1000 pertanyaan test berlabel manusia: minimal 900 answerable dan 100 unanswerable. Setiap slice memiliki minimal 100 pertanyaan; label slice boleh tumpang tindih. Variasi typo/informal/code-switch dari pertanyaan dasar yang sama tidak dipisahkan antar train/dev/test. Label harus mencakup pasal/versi dan set bukti minimum yang sah, bukan hanya jawaban teks.

Abstention pada query answerable dihitung salah untuk akurasi. Penolakan keliru maksimal 2%; recall abstention query tanpa bukti minimal 98%. Citation precision mengukur dukungan semantik klaim, bukan sekadar URL yang ada. Temporal accuracy mencakup status unknown berlabel. Setiap syarat/pengecualian yang esensial termasuk dalam rubrik jawaban.

Graph quality memakai minimal 5000 relasi dan 10.000 mention berlabel, dengan pasangan same-entity serta confusing-different-entity yang memadai. Relation precision >=98%, recall >=90%; canonical pair precision >=99,5%, recall >=97%; blocking recall >=99,5%. Predicate, arah, dan kondisi wajib dinilai. Source mapping serta endpoint integrity tidak boleh dikurangi demi throughput.

Parsing quality memakai minimal 1000 halaman berlabel, termasuk 500 halaman scan standar dan 10.000 token kritis. F1 struktur >=99%, ketepatan nomor/negasi/pengecualian >=99,9%, CER scan standar <=1%. Setidaknya 100 halaman scan sulit tetap dilaporkan terpisah; scope scan standar ditetapkan sebelum hasil dilihat. Parity native/reference membatasi penurunan Recall@20/nDCG@10 masing-masing 0,5 poin persentase, dan target kualitas absolut tetap berlaku.

## Status hasil dan negosiasi

**NOT_MEASURED** berarti belum ada hasil. **BLOCKED** berarti prasyarat seperti dataset, model identity, atau workload tidak lengkap. **FAIL** berarti run yang valid melewati salah satu batas required. **PASS** hanya jika seluruh gate applicable dan prasyarat terpenuhi. Nilai kosong atau nol sampel tidak menjadi PASS.

Hybrid GraphRAG adalah profil release yang harus memenuhi semua gate applicable. Vector RAG, Hybrid RAG, dan GraphRAG tetap baseline pembanding; kegagalannya dicatat dan tidak otomatis memblokir release Hybrid GraphRAG. Model dan parameter tidak boleh diganti di tengah test tanpa membuat run manifest baru.

Jika target gagal, terus perbaiki implementasi, lakukan profiling/optimasi, dan uji ulang sampai target tercapai tanpa meminta izin untuk pekerjaan perbaikan dalam scope. FAIL bukan alasan berhenti dan tidak otomatis memicu negosiasi benchmark.

Persetujuan pengguna hanya diperlukan untuk mengubah benchmark: angka target, workload, asumsi penerimaan, atau kriteria lulus. Jika mengusulkannya, sertakan target dan nilai aktual, kondisi run, bottleneck terukur, optimasi yang sudah dicoba, opsi perbaikan, dampak kualitas/biaya, serta perubahan benchmark yang diusulkan. Benchmark lama tetap berlaku sampai perubahan disetujui; perbaikan yang tidak terblokir tetap berjalan. Jangan menurunkan target, mengurangi beban, menghapus query sulit, atau menandai lulus secara sepihak.

Persetujuan perubahan dicatat dengan suite version baru dan keputusan arsitektur; raw results, hash konfigurasi lama, serta hasil gagal tetap disimpan. Prioritas pengguna untuk kecepatan tidak membenarkan penyembunyian penurunan kualitas.

## Integrasi

[configs/evaluation.yaml](../configs/evaluation.yaml) menunjuk suite ini. Docstring komponen merujuk file YAML yang sama agar angka tidak bercabang. Runner dan evaluator gate belum diimplementasikan; langkah saat ini menetapkan kontrak required yang akan diterapkan pada runner. Biaya moneter, cold start, scan sulit, dan stress skala lebih besar tetap dilaporkan sebagai metrik informasional tanpa menyamarkannya sebagai gate yang sudah dipenuhi.
