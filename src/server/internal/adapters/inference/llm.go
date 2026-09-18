// Menyediakan kontrak adapter generation dan structured output lintas provider LLM melalui API inference.
//
// Peran dalam komponen:
// Dipakai extraction, resolution, summarization, dan answering sesuai tugasnya.
//
// Kontrak integrasi dan perhatian implementasi:
// Provider aktual belum dipilih; timeout, retry, rate limit, schema validation, dan token usage harus terlihat ke caller.
//
// Benchmark dan gate penerimaan:
// [MODEL] Ukur cold load terpisah dari inference warm, throughput token/embedding/pasangan, p50/p95, RAM/VRAM, truncation, dan biaya. Catat model/version, precision, hardware, dan batch size. Cache harus memasukkan versi model serta input yang relevan; target numerik wajib mengikuti configs/benchmark-targets.yaml.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
//
// Batas runtime: file Go ini adalah client inference. Eksekusi tensor berada di src/inference atau provider eksternal; tidak ada pemuatan model lokal di handler Go.
package inference
