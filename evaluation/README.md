# evaluation

Benchmark offline untuk menilai empat pendekatan RAG, kualitas graph, citation, latency, dan biaya. Evaluasi menggunakan komponen produksi yang sama dengan konfigurasi berbeda. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Tidak berada pada critical path request pengguna dan tidak menjadi pipeline produksi kedua. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../AGENTS.md).

## Peran dan integrasi anak

Runner Python menggunakan endpoint Go atau artefak terstruktur dari runtime produksi sesuai src/contracts, bukan import regulagraph Python. Anak memakai corpus snapshot, dataset version, konfigurasi, dan model version yang tercatat. Pisahkan development/test, cegah kebocoran variasi pertanyaan, dan simpan hasil di artifacts.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Subfolder yang dikelola: [datasets/](datasets/README.md), [experiments/](experiments/README.md), [metrics/](metrics/README.md).

Berkas langsung: [__init__.py](__init__.py), [config.py](config.py), [gates.py](gates.py),
[telemetry.py](telemetry.py), dan [runner.py](runner.py). Format bundle, perintah, artefak hasil, serta batas
klaim dijelaskan dalam [panduan runner E01](../doc/evaluation-runner.md).

## Benchmark dan perhatian kualitas

**EVAL.** Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

Lihat [kebijakan benchmark](../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Loader konfigurasi/profile, primitive metrik, telemetry observation, evaluator 59 gate, dan runner artefak
E01 sudah aktif. Runner memvalidasi bundle hasil komponen produksi; ia belum menjalankan workload Go/Rust/C++
atau menghasilkan gold label. Karena pipeline, model, corpus terstruktur, dan gold acceptance belum lengkap,
target tetap **REQUIRED_UNMEASURED**. Unit/integration fixture hanya membuktikan evaluator menolak false PASS.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [runner.py](runner.py) | Hubungkan adapter workload produksi agar menghasilkan bundle frozen; pertahankan evaluator offline dan format immutable. | Jalankan retrieval/answer load generator nyata, cocokkan jumlah arrival, serta audit hash dan output pada tiga run. |
| [telemetry.py](telemetry.py) | Tambahkan timestamp token client untuk gate inter-token ketika kontrak C01 diperluas secara terkoordinasi. | Uji buffering, token kosong/heartbeat, stream putus, timeout, dan clock monotonic. |
| [gates.py](gates.py) | Tambahkan interval bootstrap terkelompok dari gold scorer tanpa mengubah threshold atau denominator. | Uji base-question grouping, slice overlap, sample minimum, dan reproducibility seed. |
