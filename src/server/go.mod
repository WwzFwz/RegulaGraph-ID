// Metadata modul Go untuk serving dan coordinator RegulaGraph-ID.
// Integrasi: seluruh komponen internal tetap dalam satu module; belum ada SDK runtime.
// Performa: build tidak memuat model atau memulai layanan; benchmark menggunakan runtime aktif kelak.
module regulagraph.local/server

go 1.22.0
