// Mengubah hasil traversal menjadi Evidence beserta rantai relasi dan teks sumber.
//
// Peran dalam komponen:
// Menjembatani graph retrieval ke fusion dan context builder.
//
// Kontrak integrasi dan perhatian implementasi:
// Pasal penghubung tidak dibuang hanya karena skor individual rendah; deduplikasi mempertahankan jalur dan semua provenance yang diperlukan.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
//
// [CONTEXT] Ukur cakupan gold evidence, kelengkapan jalur graph, duplikasi, token count, dan waktu membangun konteks. Gate: tiap item konteks dapat dipetakan ke sumber dan versi; pemotongan/ketidakcukupan bukti dilaporkan. Context budget tidak boleh diam-diam menghapus syarat penting.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Hydrate selected paths in batches into primary source/version spans and explicit missing dependencies.
// Bukti verifikasi: Test unsupported edges, stale versions and source mismatch; profile database round trips and required-path coverage.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package graph
