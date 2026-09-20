"""Tooling corpus offline tanpa side effect import.

Peran: menampung sampling dan profiler M01; runtime produksi tidak mengimpor package ini.
Kontrak: fungsi publik harus mengikat inventory/config dan menulis artefak terverifikasi.
Benchmark: import tidak membaca corpus; pengukuran hanya berjalan lewat entry point eksplisit.
Status: package aktif untuk baseline profiling PDF.
"""
