# src/server/internal/retrieval/query

Persiapan pertanyaan melalui normalisasi, klasifikasi, dan pengaitan penyebutan ke entitas graph yang sudah ada. Implementasi runtime berada di Go. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengubah canonical registry atau menebak fakta yang tidak ada dalam pertanyaan. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak mempertahankan pertanyaan asli, nomor, tahun, negasi, dan filter waktu. Ketidakpastian linking harus diteruskan; kegagalan satu jalur tidak otomatis menutup jalur retrieval lain.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [classifier.go](classifier.go), [entity_linker.go](entity_linker.go), [normalizer.go](normalizer.go).

## Benchmark dan perhatian performa

**RETRIEVAL.** Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml); jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.

**RESOLUTION.** Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status lintas repositori: collector PDF dan kontrak/validator C01 sudah tersedia; lihat [cakupan implementasi C01](../../../../../doc/contracts-implementation.md). Pipeline parsing/graph/retrieval, adapter storage, layanan model dan evaluator benchmark masih belum aktif. Status anak dijelaskan pada header masing-masing; build dan fixture tidak membuktikan target kualitas atau latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [classifier.go](classifier.go) | Resolve query intent/temporal mode into an auditable RetrievalPlan with an uncertainty path. | Evaluate per-intent confusion and routing cost; ambiguity must not silently pick a legal date or skip relevant retrieval branches. |
| [entity_linker.go](entity_linker.go) | Resolve query mentions against snapshot-pinned canonical registry and aliases with scope and confidence. | Test homonyms, same article number across laws and unresolved mentions; measure candidate recall and false merges. |
| [normalizer.go](normalizer.go) | Preserve original question and normalize mechanical variants while keeping negation, article numbers, years and quoted terms. | Test informal/typo/Indonesian-English cases and destructive normalization counterexamples; record original-to-normalized trace. |
