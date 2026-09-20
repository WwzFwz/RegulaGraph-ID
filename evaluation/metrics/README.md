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

Primitive deterministik untuk runtime, retrieval, graph, citation, dan answer sudah aktif sebagai bagian E01.
Mereka menerima evidence/count hasil produksi dan tidak membuat label atau memanggil model. Hasil produksi
belum tersedia, sehingga target tetap **REQUIRED_UNMEASURED**. Header tiap file menyatakan denominator,
peran, kasus gagal, dan bukti verifikasi yang wajib dipertahankan sebelum perubahan.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [answers.py](answers.py) | Integrasikan reviewed label untuk ambiguity/conflict dan kalibrasi judge terhadap manusia. | Uji ambiguous/no-answer/conflict serta uncertainty tanpa tuning pada test labels. |
| [citations.py](citations.py) | Hubungkan claim-level review dan acceptable evidence set dari scorer dataset. | Uji multiple valid sets, locator/version salah, unsupported claim, dan missing annotation. |
| [graph.py](graph.py) | Tambahkan matcher typed relation/qualifier/provenance sebelum count dikirim ke primitive. | Uji negation, homonym, merge/split, arah, qualifier, dan shared support. |
| [retrieval.py](retrieval.py) | Hubungkan production ranked output dan gold provision-version/path ke agregasi runner. | Uji ties policy, truncation, unanswerable, base group, dan seluruh chain multi-hop. |
| [runtime.py](runtime.py) | Tambahkan agregasi memory/cost dan confidence interval yang format evidencenya dibekukan. | Uji price source/date, clock unit, timed-out/missing request, dan coordinated omission. |
