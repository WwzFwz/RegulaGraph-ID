// Metadata modul Go untuk serving dan coordinator RegulaGraph-ID.
// Integrasi: seluruh komponen internal satu module; x/net/html mem-parsing halaman sumber collector,
// pgx menyediakan pool/transaksi PostgreSQL S01, dan protobuf membawa kontrak wire C01.
// Performa: build tidak memuat model atau memulai layanan; benchmark menggunakan runtime aktif kelak.
module regulagraph.local/server

go 1.26.0

toolchain go1.26.8

require (
	github.com/jackc/pgx/v5 v5.11.0
	golang.org/x/net v0.59.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
