# evaluation/datasets

Dataset berlabel untuk pertanyaan, jawaban rujukan, bukti pasal/versi, hubungan graph, dan pertanyaan tanpa dukungan corpus. Dokumen ini menjadi kontrak cakupan folder dan panduan penempatan komponennya.

## Batas tanggung jawab

Dokumen mentah besar berada di data; output model berada di artifacts. Komponen baru harus sesuai definisi cakupan di atas. Jika tidak sesuai, jelaskan fungsi serta lokasi yang diusulkan kepada pengguna dan tanyakan apakah perlu folder baru atau perluasan cakupan folder ini sebelum menempatkannya. Ikuti [aturan repositori](../../AGENTS.md).

## Peran dan integrasi anak

Anak memiliki schema dan provenance label, reviewer, group ID, serta split. Parafrasa, typo, dan code-switch dari pertanyaan dasar yang sama harus berada pada split yang sama.

Ikuti [rencana corpus dan gold](../../doc/corpus-plan.md) untuk sumber BPK/Kemkomdigi/JDIHN, acceptable evidence sets, temporal labels, dan review manusia. [Kontrak evaluasi](../../doc/system-contracts.md) menetapkan manifest dataset/run serta observasi/gate. Data mentah di data berbeda dari gold berlabel di sini; label test tidak dipakai untuk tuning.

Anak tidak boleh mengubah kontrak input/output secara tersembunyi. Perubahan bentuk data, identitas, versi, atau penanganan error didokumentasikan bersama konsumennya. File implementasi menjelaskan perhatian spesifiknya pada docstring atau komentar pembuka; aturan induk tetap berlaku.

## Isi saat ini

Berkas langsung: [__init__.py](__init__.py), [schema.py](schema.py), dan [loader.py](loader.py).
[annotation-guide.md](annotation-guide.md) memberi urutan label parsing/graph/query, review manusia,
adjudikasi, split, dan freeze terhadap kontrak `GoldQuestion`; belum ada gold berlabel aktif.

## Benchmark dan perhatian kualitas

**EVAL.** Gate: setiap run merekam dataset/split, corpus snapshot, model/prompt/config version, seed bila relevan, serta biaya. Laporkan ukuran sampel dan ketidakpastian, hindari tuning pada test set, dan pisahkan kualitas retrieval, graph, jawaban, serta waktu/biaya.

Lihat [kebijakan benchmark](../../doc/benchmark-policy.md) untuk protokol pengukuran dan penetapan angka target. Target numerik wajib berada di [target numerik wajib](../../configs/benchmark-targets.yaml) dengan status **REQUIRED_UNMEASURED**; belum ada hasil yang diklaim tercapai. Gate deterministik berlaku pada data yang diterbitkan dan fixtures yang relevan; hasil semantik tetap memerlukan evaluasi.

## Status implementasi

Kontrak/validator manifest C01 dan konsumsi manifest oleh runner E01 sudah aktif. Gold dataset manusia,
versioned loader pertanyaan, serta hasil acceptance produksi belum tersedia; fixture sintetis tidak
membuktikan kualitas model.
Antrean kandidat PDF di [tooling/corpus](../../tooling/corpus/README.md) hanya membantu memilih bahan
anotasi. Baris UNREVIEWED tidak boleh dimuat sebagai GoldQuestion atau dibagi train/dev/test sebelum
snapshot corpus, guideline, label, dan review manusia dibekukan.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [schema.py](schema.py) | Pertahankan generated descriptor sebagai sumber constraint C01 dan perluas hanya bersama perubahan schema. | Uji duplicate/group leakage, incomplete review, snapshot/model drift, batas ukuran, dan lossy conversion. |
| [loader.py](loader.py) | Hubungkan scorer gold production setelah G01 tersedia; pertahankan loader JSONL dan corpus manifest sebagai eligibility boundary. | Uji dataset besar, slice overlap, answerable tanpa gold, unanswerable dengan evidence, serta corpus identity/count mismatch. |

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [evaluation.proto](evaluation.proto). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../doc/contracts-implementation.md).
