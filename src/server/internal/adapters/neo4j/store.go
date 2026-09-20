// Menyediakan adapter penulisan graph dan eksekusi query yang diminta komponen.
//
// Peran dalam komponen:
// Menghubungkan assembly serta traversal ke Neo4j.
//
// Kontrak integrasi dan perhatian implementasi:
// Gunakan parameter dan identitas stabil; pembatasan resource query harus terlihat ke caller, bukan diam-diam menghilangkan bukti.
//
// Benchmark dan gate penerimaan:
// [GRAPH] Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.
//
// [STORAGE] Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: scaffold dokumentasi; perilaku modul belum diimplementasikan.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Implement parameterized batched graph writes/readback with corpus, visibility and support identity constraints.
// Bukti verifikasi: Test replay, shared-support retention and search-ready receipts on real Neo4j; profile query plans and fan-out.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package neo4j
