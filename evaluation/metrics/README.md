# evaluation/metrics

Implementasi ukuran kualitas retrieval, ekstraksi, resolution, jawaban, citation, latency, dan biaya. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak memperlakukan skor LLM judge sebagai kebenaran hukum atau benchmark sebagai generator jawaban produksi. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Anak menyatakan denominator, unit, kasus tanpa label, dan agregasi per kategori. Predicate, versi pasal, kelengkapan bukti multi-hop, serta abstention dinilai tersendiri; kalibrasikan judge terhadap penilaian manusia.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Berkas langsung: [__init__.py](__init__.py), [answers.py](answers.py), [citations.py](citations.py), [graph.py](graph.py), [retrieval.py](retrieval.py), [runtime.py](runtime.py).

## Benchmark dan perhatian kualitas

**EVAL.** Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answers.py](answers.py) | Score correctness, temporal correctness, faithfulness and abstention separately; calibrate any judge against human labels. | Test ambiguous/no-answer/conflict cases and invalid denominator; report category counts and uncertainty without tuning on test labels. |
| [citations.py](citations.py) | Score claim-level citation precision/coverage and correct version/locator against acceptable evidence sets. | Distinguish syntactic references from actual semantic support; test multiple valid evidence sets, unsupported claims and missing annotations. |
| [graph.py](graph.py) | Evaluate typed extraction, qualifiers, canonical resolution and supported multi-hop paths with explicit gold matching rules. | Test negation, homonyms, merge/split and shared supports; report candidate recall separately from final resolution precision. |
| [retrieval.py](retrieval.py) | Compute Recall@k, nDCG@k and all-required-evidence/path coverage using production ranked output. | Test duplicates, ties, multiple acceptable sets and unanswerable questions; keep denominators and truncation policy explicit. |
| [runtime.py](runtime.py) | Aggregate monotonic queue/compute/TTFT/total traces, throughput, errors, memory and cost across the frozen workload. | Test percentile calculation, clock-unit mismatch and timed-out/missing requests; avoid coordinated-omission bias and success-only latency. |
