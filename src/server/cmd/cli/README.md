# src/server/cmd/cli

Entry point perintah ingestion, update, serta query melalui komponen Go yang sama. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Logika di luar cakupan ini ditempatkan pada komponen pemiliknya. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Perintah collect memanggil internal/workflows.CollectSources dan mengunduh PDF nyata serta metadata ke data/acquisition. Perintah audit memanggil sources.AuditAcquisition untuk memverifikasi inventory lokal secara streaming dan menulis manifest deterministik. CLI hanya memuat argumen, merakit dependency, dan melaporkan output JSON. Exit code 0 berarti operasi/integrity sukses, 1 berarti kegagalan atau integrity error, dan 2 berarti argumen salah. Perintah ingestion/update/query produksi belum tersedia. Lihat [panduan collector](../../../../doc/acquisition.md).

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [main.go](main.go), [main_test.go](main_test.go), [audit.go](audit.go), dan [audit_test.go](audit_test.go).

Perintah discover menerima seed katalog dan memanggil workflows.DiscoverSources untuk menghasilkan antrean persisten tanpa unduhan PDF. Input collect tetap URL detail/PDF; jangan menukar kedua jenis file. Lihat configs/listings.txt untuk seed katalog.

## Benchmark dan perhatian performa

Prioritas: p95/p99 latency query, waktu antre dan time-to-first-answer-token; untuk job ukur throughput serta peak RSS. Target numerik wajib ada di [target numerik wajib](../../../../configs/benchmark-targets.yaml); profil referensi dan status REQUIRED_UNMEASURED berlaku. Pemrosesan berjalan tanpa loading model per request dan tanpa RPC per tahap kecil fusion/filter/context.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Perintah discover, collect, dan audit inventory D01 sudah aktif. Kontrak/validator C01, evaluator E01, serta fondasi storage S01 juga tersedia pada komponen pemiliknya. Ingest/update/query end-to-end, parsing/graph/retrieval, dan layanan model belum aktif. Audit integrity tidak membuktikan kualitas isi PDF, canonical identity, atau target performa.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [main.go](main.go) | Pertahankan routing command tipis; expose job/status/update/query hanya setelah workflow pemilik aktif. | Uji exit code, machine-readable output, cancellation, dan Ctrl+C pada proses aktif. |
| [audit.go](audit.go) | Pertahankan flag bounded dan exit integrity; tambahkan opsi output hanya bila format manifest tetap kompatibel. | Uji root/write/stdout failure, cancellation, dan invalid inventory tidak pernah exit 0. |
