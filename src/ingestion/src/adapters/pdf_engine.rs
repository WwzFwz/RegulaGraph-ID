//! Boundary engine PDF native C/C++.
//!
//! Peran dalam komponen:
//! Menyediakan akses parsing bagi document/parsing.
//!
//! Integrasi dan perhatian performa:
//! Engine belum dipilih; worker mengatur unit paralelisme sesuai kemampuan library. Kepemilikan buffer, lifetime, urutan baca, dan offset sumber harus eksplisit.
//!
//! Benchmark dan gate penerimaan:
//! Ukur p50/p95/p99, throughput, waktu antre, serta RSS/VRAM sesuai workload. Ambang wajib ada di configs/benchmark-targets.yaml (REQUIRED_UNMEASURED); ukur cold/warm terpisah dan pertahankan kualitas sumber/versi.
//!
//! Status: scaffold; belum ada pipeline atau layanan yang aktif.
