// Memeriksa bentuk jawaban, keabsahan referensi, konflik bukti, dan kecukupan dukungan.
//
// Peran dalam komponen:
// Menjadi pemeriksaan setelah generation sebelum hasil diterbitkan.
//
// Kontrak integrasi dan perhatian implementasi:
// Pisahkan validasi referensi dari penilaian semantik; jika memakai judge model, ukur tambahan latency/biaya dan kalibrasinya.
//
// Benchmark dan gate penerimaan:
// [ANSWER] Ukur ketepatan jawaban, citation precision/coverage, ketepatan versi, abstention pada pertanyaan tanpa bukti, faithfulness, latency p50/p95/p99, dan token/biaya. Gate deterministik hanya validitas referensi; dukungan semantik harus dievaluasi tersendiri dengan review manusia terkalibrasi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
package answering
