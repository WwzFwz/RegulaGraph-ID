// Mendefinisikan bentuk kesalahan HTTP yang konsisten dan dapat dilacak.
//
// Peran dalam komponen:
// Membantu klien membedakan input invalid, dependency gagal, dan pekerjaan belum selesai.
//
// Kontrak integrasi dan perhatian implementasi:
// Jangan mengirim kredensial, stack trace sensitif, atau keluaran vendor mentah; bawa request ID untuk diagnosis.
//
// Benchmark dan gate penerimaan:
// [API] Gate: input/response mengikuti schema, error tidak disamarkan menjadi jawaban sukses, dan readiness sesuai dependency yang diperlukan. Ukur latency end-to-end p50/p95, error rate, concurrency, dan timeout pada workload yang dinyatakan; target benchmark wajib ada di configs/benchmark-targets.yaml; hasil belum diukur.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Map domain/vendor failures to stable safe error codes and retry hints, preserving trace IDs.
// Bukti verifikasi: Test malformed input, timeout, cancellation and conflict mapping; never serialize credentials or raw vendor errors.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package schemas
