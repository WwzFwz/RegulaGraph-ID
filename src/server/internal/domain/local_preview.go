// Local preview values carry page provenance for the interview RAG baseline.
// They are in-process/UI values, not replacement C01 worker/index contracts.
// Page byte offsets refer to hash-verified UTF-8 exports. A PDF hash is not a
// canonical regulation ID; no effective-date or graph assertion is inferred.
// Quality and latency gates remain NOT_MEASURED under benchmark-targets.yaml.
package domain

type PreviewPage struct {
	BlobSHA256 string
	Title      string
	URL        string
	Page       int
	Text       string
}

type PreviewPassage struct {
	ID         string  `json:"id"`
	BlobSHA256 string  `json:"blob_sha256"`
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Page       int     `json:"page"`
	Text       string  `json:"text"`
	StartByte  int     `json:"start_byte"`
	EndByte    int     `json:"end_byte"`
	Score      float64 `json:"score"`
}

type PreviewClaim struct {
	Text      string   `json:"text"`
	SourceIDs []string `json:"source_ids"`
}

type PreviewAnswer struct {
	Status       string           `json:"status"`
	Claims       []PreviewClaim   `json:"claims"`
	Sources      []PreviewPassage `json:"sources"`
	Model        string           `json:"model"`
	RetrievalMS  float64          `json:"retrieval_ms"`
	GenerationMS float64          `json:"generation_ms"`
	Notice       string           `json:"notice"`
}
