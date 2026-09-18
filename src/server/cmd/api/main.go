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
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "RegulaGraph api: scaffold only; runtime is not implemented.")
	os.Exit(2)
}
