// Menjadi endpoint tanya jawab yang memanggil workflow answer.
//
// Peran dalam komponen:
// Menyajikan hasil dan citation melalui schema HTTP.
//
// Kontrak integrasi dan perhatian implementasi:
// Tidak memanggil database/model secara langsung; deadline dan cancellation perlu diteruskan tanpa menyamarkan jawaban parsial.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
//
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Validate/authenticate question requests, call answer workflow once and stream typed events with backpressure.
// Bukti verifikasi: Test client disconnect, exactly one terminal event, partial generation and unavailable snapshot; record TTFT and total latency.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package routes
