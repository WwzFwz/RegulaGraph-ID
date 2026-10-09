# src/ingestion/src/domain

Definisi data dan invariant lintas komponen: dokumen, versi pasal, chunk, canonical entity, relasi, bukti, dan jawaban. Domain menjadi bahasa bersama kedua alur ingestion dan tanya jawab. Representasi lokal Rust mengacu pada src/contracts lintas runtime; definisi wire tidak digandakan. Dokumen ini mendefinisikan superset tanggung jawab folder dan kontrak integrasi anaknya.

## Batas tanggung jawab

Tidak mengandung client database, prompt model, routing HTTP, atau orchestration. Jika fungsi baru tidak sesuai cakupan, siapkan usulan lokasi dan alasan lalu tanyakan kepada pengguna apakah perlu folder baru atau perluasan cakupan. Migrasi runtime ini sudah disetujui; pekerjaan rutin yang sesuai definisi tidak perlu konfirmasi ulang. Ikuti [AGENTS.md](../../../../AGENTS.md).

## Peran dan integrasi anak

Semua anak memakai ID stabil, schema version, dan referensi sumber yang eksplisit. Kontrak tidak mengimpor SDK eksternal; perubahan kontrak harus ditinjau terhadap seluruh konsumennya.

Pertahankan source/canonical/provision-version/snapshot ID dan schema version lintas anak. Boundary runtime mengikuti [src/contracts](../../../contracts/README.md), dengan pekerjaan batch atau inference yang jelas. Perubahan bentuk data, error/status, serta offset sumber harus didokumentasikan bersama konsumennya; jangan menggandakan kebijakan publikasi di beberapa runtime.

## Isi saat ini

Scaffold tipe dokumen/entitas/evidence/relasi yang tidak memiliki perilaku telah
dihapus. Record wire tetap berasal dari C01; [document_batch.rs](document_batch.rs)
memvalidasi dokumen dan [chunk_provenance.rs](chunk_provenance.rs) memproyeksikan
bukti. Transformasi entitas/relasi dimiliki [assembly](../knowledge_graph/assembly/README.md),
sedangkan authority canonical tetap registry Go. Rekomendasi di bawah menjaga
ID/revision, versi historis, span dan shared support pada pemilik fungsi aktual;
penghapusan scaffold tidak menandai merge/split atau versioning lengkap selesai.

Berkas: [chunks.rs](chunks.rs), [chunk_provenance.rs](chunk_provenance.rs), [document_batch.rs](document_batch.rs), [document_wire.rs](document_wire.rs), [mod.rs](mod.rs), [text_artifact_wire.rs](text_artifact_wire.rs), dan [wire.rs](wire.rs).

## Benchmark dan perhatian performa

**DOMAIN.** Gate deterministik: ID dan referensi sumber/versi tidak ambigu; invalid record ditolak atau dikarantina secara eksplisit. Kompatibilitas schema diperiksa dengan fixtures; belum ada hasil pengukuran.

Ikuti [kebijakan benchmark](../../../../doc/benchmark-policy.md). Angka wajib mengikuti [target numerik wajib](../../../../configs/benchmark-targets.yaml) dengan profil asumsi yang dinyatakan; statusnya **REQUIRED_UNMEASURED** sampai diuji. Ukur waktu antre serta p95/p99 selain throughput; validitas source/version dan ketepatan bukti tetap menjadi syarat optimasi.

## Status

`chunk_provenance.rs` memproyeksikan source blob, versi pasal, span, dan halaman yang sama untuk EXTRACT serta persiapan INDEX. Ia memeriksa ikatan `version.text_ref` ke teks normalisasi, seluruh span versi pada artefak yang sama, cakupan node pemilik/versi, rantai parent, dan halaman sumber yang benar. Hanya halaman sukses yang overlap chunk diteruskan; bila node tidak punya locator, proyeksi menambahkan locator halaman tanpa kotak koordinat dari `PageResult`. Ukur p95 proyeksi dan peak RSS pada PDF panjang; target required tetap belum diukur.

`chunks.rs` menyediakan record lokal dan validator provenance/source mapping/token count. `document_wire.rs` memproyeksikan structure/chunk dan mempertahankan provision-version berbeda per structure node; `text_artifact_wire.rs` memproyeksikan mapping/page/parser dan normalizer manifest; `document_batch.rs` merakit serta memvalidasi ulang batch dengan typed reference closure. Wire validator C01 dan assembler dipakai executable worker PARSE/STRUCTURE/CHUNK. Reconstruction versioning lanjutan, graph, dan index belum diimplementasikan.

## Rekomendasi implementasi anak

Pekerjaan berikut melanjutkan cakupan folder ini. Header file mempertahankan status aktif/scaffold; tabel bukan klaim fitur sudah tersedia. Integrasikan keluaran anak melalui kontrak induk dan jalankan [protokol verifikasi](../../../../doc/verification.md) sebelum menyatakan paket selesai. Target angka tetap bersumber dari configs/benchmark-targets.yaml.

| File | Pekerjaan berikutnya | Bukti yang perlu disiapkan |
| --- | --- | --- |
| [chunks.rs](chunks.rs) | Pertahankan invariant record lokal ketika tokenizer atau kebijakan overlap berkembang; hindari menambahkan schema wire paralel. | Uji boundary Unicode, overlap, overflow, dan alokasi batch besar terhadap konfigurasi produksi. |
| [chunk_provenance.rs](chunk_provenance.rs) | Jadikan proyeksi bukti bersama untuk EXTRACT dan INDEX; perubahan aturan coverage harus diuji pada keduanya. | Uji versi bercampur artefak, raw-vs-normalized ref, struktur asing, halaman palsu, serta p95 dan peak RSS pada PDF panjang. |
| [document_batch.rs](document_batch.rs) | Hubungkan hasil reconstruction versioning dan metadata sumber nyata, lalu ukur assembly batch besar. | Uji dependency external/incremental, adversarial reference graph, cross-language decode, batas record/edge, p95/p99, dan peak RSS. |
| [document_wire.rs](document_wire.rs) | Hubungkan tokenizer/model manifest produksi dan locator halaman ke structure/chunk projection. | Uji golden wire lintas bahasa, missing refs, overflow, dan alokasi batch besar. |
| [document_batch.rs](document_batch.rs) | Pertahankan validasi dokumen/source/provision dari generated types; observation time tetap terpisah dari tanggal berlaku. | Test stable IDs, raw/normalized mappings and historical version ambiguity; avoid redundant conversion/allocation across batches. |
| [builder.rs](../knowledge_graph/assembly/builder.rs) | Konsumsi keputusan canonical berscope/revision dari registry; assembly tidak menetapkan merge/split atau menulis registry sendiri. | Test alias ambiguity, merge/split lineage and deterministic identity comparison; avoid redundant conversion/allocation across batches. |
| [chunk_provenance.rs](chunk_provenance.rs) | Pertahankan source/version/span dan konteks induk untuk bukti chunk; evidence path query dimiliki runtime Go. | Test corpus/version/snapshot mismatches and missing support hydration; avoid redundant conversion/allocation across batches. |
| [canonical.rs](../knowledge_graph/assembly/canonical.rs) | Pertahankan assertion/support identity dan qualifier tanpa menggabungkan bukti independen; uji ulang saat withdrawal berkembang. | Test endpoint types, source withdrawal and negation/condition preservation; avoid redundant conversion/allocation across batches. |
| [text_artifact_wire.rs](text_artifact_wire.rs) | Tambahkan hasil OCR dan locator yang telah direkonsiliasi tanpa menyamarkan halaman parsial. | Uji mapping besar, mixed text/OCR, cross-language decode, hash mismatch, dan peak RSS. |

## Penambahan C01 dan panduan verifikasi

Berkas terkait: [wire.rs](wire.rs). Dependency, cara menjalankan dan batas pembuktiannya mengikuti [implementasi C01](../../../../doc/contracts-implementation.md).
