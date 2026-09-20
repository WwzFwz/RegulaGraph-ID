// Entry point scaffold server HTTP dan streaming jawaban.
//
// Peran dalam komponen:
// Menyiapkan lokasi composition root aplikasi Go.
//
// Integrasi dan perhatian performa:
// Saat ini hanya melaporkan bahwa implementasi belum tersedia dan keluar dengan kode 2; tidak membuka port atau koneksi.
//
// Benchmark dan gate penerimaan:
// Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//
// Status: scaffold; belum ada pipeline atau layanan yang aktif.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Bootstrap config, lifecycle and API dependencies when A01/O01 endpoints exist; return explicit readiness failures.
// Bukti verifikasi: Test startup rollback, signals and graceful drain; no model load or connection at import.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "RegulaGraph api: scaffold only; runtime is not implemented.")
	os.Exit(2)
}
