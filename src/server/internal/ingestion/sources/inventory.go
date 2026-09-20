// Inventory lokal akuisisi D01: metadata portal, provenance, dan receipt PDF.
// Peran: artefak kerja sebelum kontrak produksi C01; bukan schema wire regulasi final.
// Integrasi/performa: ukuran, waktu, status dan hash dicatat; hash blob bukan ID regulasi.
// Rekomendasi implementasi berikutnya (belum merupakan fitur aktif):
// Preserve versioned receipt/history and checksum-based reuse; map acquisition observations into production source records at S01/D01 integration.
// Bukti verifikasi: Test metadata schema change, missing/corrupt artifacts and atomic latest-pointer update; never drop historical receipts.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package sources

import "time"

type PDFLink struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind"`
}
type Page struct {
	Title          string              `json:"title"`
	Fields         map[string][]string `json:"portal_fields"`
	PDFs           []PDFLink           `json:"pdf_links"`
	RelatedPDFs    []PDFLink           `json:"related_pdf_links,omitempty"`
	DocumentURLs   []string            `json:"document_urls,omitempty"`
	DocumentTitles map[string]string   `json:"document_titles,omitempty"`
	NextURLs       []string            `json:"next_urls,omitempty"`
}
type PDFReceipt struct {
	URL          string    `json:"url"`
	FinalURL     string    `json:"final_url,omitempty"`
	Label        string    `json:"label,omitempty"`
	Kind         string    `json:"kind"`
	SHA256       string    `json:"sha256,omitempty"`
	Path         string    `json:"path,omitempty"`
	Bytes        int64     `json:"bytes,omitempty"`
	ContentType  string    `json:"content_type,omitempty"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
	DurationMS   int64     `json:"duration_ms"`
	Error        string    `json:"error,omitempty"`
}
type Record struct {
	SchemaVersion int          `json:"schema_version"`
	ParserVersion string       `json:"metadata_parser_version"`
	SourceURL     string       `json:"source_url"`
	FinalURL      string       `json:"final_url,omitempty"`
	Portal        string       `json:"portal"`
	FetchedAt     time.Time    `json:"fetched_at"`
	HTMLSHA256    string       `json:"html_sha256,omitempty"`
	HTMLPath      string       `json:"html_path,omitempty"`
	Metadata      Page         `json:"metadata"`
	PDFs          []PDFReceipt `json:"pdfs"`
	Status        string       `json:"status"`
	Error         string       `json:"error,omitempty"`
}
type Result struct {
	Record Record `json:"record"`
	Reused bool   `json:"reused"`
}
