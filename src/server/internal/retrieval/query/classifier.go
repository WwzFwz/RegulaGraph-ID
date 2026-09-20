// Mengidentifikasi kebutuhan pertanyaan seperti lookup, temporal, prosedural, atau multi-hop.
//
// Peran dalam komponen:
// Menyediakan sinyal routing untuk workflow answer.
//
// Kontrak integrasi dan perhatian implementasi:
// Klasifikasi keliru tidak boleh secara diam-diam menutup jalur bukti; pemanggilan LLM tambahan harus dibuktikan manfaat dan biayanya.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
//
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Resolve query intent/temporal mode into an auditable RetrievalPlan with an uncertainty path.
// Bukti verifikasi: Evaluate per-intent confusion and routing cost; ambiguity must not silently pick a legal date or skip relevant retrieval branches.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package query
