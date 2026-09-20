// Menyediakan client Go untuk embedding dokumen/pertanyaan dengan BGE-M3 sebagai kandidat baseline.
//
// Peran dalam komponen:
// Memanggil runtime native atau provider melalui kontrak inference; eksekusi tensor tidak berada dalam package Go ini.
//
// Kontrak integrasi dan perhatian implementasi:
// Bedakan dense, learned sparse, dan multi-vector; batching/truncation serta versi model dicatat, dan load tidak terjadi saat inisialisasi paket.
//
// Benchmark dan gate penerimaan:
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
//
// [INDEX] Ukur konsistensi ID/versi antarindeks, freshness lag, throughput indexing, ukuran indeks, dan retrieval Recall@k. Gate: penghapusan/upsert idempotent dan model/dimensi representasi cocok. Target kapasitas wajib mengikuti profil corpus/hardware dalam configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//
// Batas runtime: file Go ini adalah client inference. Eksekusi tensor berada di src/inference atau provider eksternal; tidak ada pemuatan model lokal di handler Go.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Reuse a native client; send purpose/model-bound batches and validate one-to-one results before retrieval/indexing.
// Bukti verifikasi: Test reordered/duplicate/missing outputs, dimension drift and explicit per-item errors; trace queue vs compute time.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package inference
