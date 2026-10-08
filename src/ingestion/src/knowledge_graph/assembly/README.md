# src/ingestion/src/knowledge_graph/assembly

Penggabungan entitas yang telah diselesaikan identitasnya dan relasi menjadi perubahan graph yang konsisten. Transformasi batch berjalan dalam Rust; inference semantik menggunakan adapter model. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengekstrak ulang makna atau menjalankan strategi pencarian pertanyaan. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../../AGENTS.md).

## Peran dan integrasi anak

Anak memetakan endpoint ke canonical ID, mempertahankan banyak bukti untuk satu relasi, dan menghasilkan perubahan idempotent. Penghapusan satu sumber tidak boleh menghapus relasi yang masih didukung sumber lain.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Berkas: [builder.rs](builder.rs), [canonical.rs](canonical.rs),
[canonical_tests.rs](canonical_tests.rs), [delta.rs](delta.rs),
[delta_tests.rs](delta_tests.rs), [inputs.rs](inputs.rs), [mod.rs](mod.rs).

`inputs.rs` menerima GraphAssemblyPlan dan RegistryEntityView C01, mengikat role sumber,
publication/fence/revision, ontology dan exact canonical selection sebelum membentuk delta.
Caller tetap memverifikasi bytes/receipt/authority; lihat [kontrak input](../../../../../doc/graph-assembly-inputs.md).

`builder.rs` kini menyediakan fungsi prapublikasi yang mengganti endpoint mention
berdasarkan keputusan LINK/CREATE RESOLVE terikat EXTRACT, memeriksa canonical yang
disediakan pembaca registry terverifikasi, dan mempertahankan seluruh support asli.
Keluaran fungsi raw masih membawa ID ekstraksi. `assemble_canonical_relations` pada
`canonical.rs` mengomposisikannya dengan dedup assertion/support dan remap exception,
serta mempertahankan mapping ID lama ke ID hasil. Input/output bytes dan jumlah
record/referensi dibatasi. `delta.rs` menggabungkan hasil tersebut dengan registry rows,
dependency manifest dan target visibility menjadi GraphDelta upsert. Source DocumentBatch,
hash/UTF-8 teks dan mention surface diperiksa; caller mengautentikasi ref/receipt sebelum
dispatch. Closure incremental, worker dan writer Neo4j masih diperlukan sebelum publikasi.
Lihat [kontrak delta](../../../../../doc/graph-delta.md).

Identitas assertion mencakup seluruh temporal scope, sehingga view knowledge berbeda
dipisahkan secara konservatif. Qualifier/kondisi, arah, origin dan ontology tidak
dihilangkan. Support menyimpan evidence, versi sumber, producer dan independent group;
duplicate extraction tidak membuat dukungan independen baru. Unknown protobuf fields
ditolak secara rekursif sebelum hashing; cyclic exception refs gagal eksplisit.
Lihat [kontrak identitas](../../../../../doc/graph-canonical-identity.md). Pemanggil
harus memverifikasi registry receipt dan bytes sumber sebelum memakai library.

## Benchmark dan perhatian performa

**GRAPH.** Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.

**UPDATE.** Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent; dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia. Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap full rebuild serta freshness lag.

Ikuti [kebijakan benchmark](../../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

Status subkomponen ini endpoint, identitas graph deterministik dan GraphDelta upsert;
ASSEMBLE worker, mutasi backend, retrieval graph, gold dataset, dan
acceptance produksi belum aktif. Status anak dijelaskan pada header masing-masing;
tes library tidak membuktikan kebenaran semantik atau target latency.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [builder.rs](builder.rs), [canonical.rs](canonical.rs), [delta.rs](delta.rs) | Sambungkan upsert delta ke worker dan view registry ber-receipt; lanjutkan closure incremental. | Integrasikan penarikan support pada backend, exact receipt, dangling edge, dan full-rebuild equivalence. |
