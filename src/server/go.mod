// Metadata modul Go untuk serving dan coordinator RegulaGraph-ID.
// Integrasi: seluruh komponen internal satu module; x/net/html mem-parsing halaman sumber collector.
// Performa: build tidak memuat model atau memulai layanan; benchmark menggunakan runtime aktif kelak.
module regulagraph.local/server

go 1.26.0

toolchain go1.26.8

require golang.org/x/net v0.59.0

require google.golang.org/protobuf v1.36.12 // indirect
