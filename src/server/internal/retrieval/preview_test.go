// Verifies local preview ranking and exact UTF-8 page offsets on overlapping
// windows, with empty/OOV/cancelled inputs. Synthetic relevance is not gold.
package retrieval

import (
	"context"
	"regulagraph.local/server/internal/domain"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreviewRankingAndSourceOffsets(t *testing.T) {
	text := strings.Repeat("Kewajiban menjaga kerahasiaan informasi résumé. ", 70)
	pages := []domain.PreviewPage{{BlobSHA256: "a", Title: "Data pribadi", Page: 1, Text: text}, {BlobSHA256: "b", Title: "Frekuensi radio", Page: 2, Text: strings.Repeat("Spektrum frekuensi radio telekomunikasi. ", 30)}}
	index, err := NewPreviewIndex(pages)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range index.passages {
		source := pages[0].Text
		if p.BlobSHA256 == "b" {
			source = pages[1].Text
		}
		if !utf8.ValidString(p.Text) || source[p.StartByte:p.EndByte] != p.Text {
			t.Fatal("source offsets changed")
		}
	}
	for run := 0; run < 2; run++ {
		hits, e := index.Search(context.Background(), "Apa kewajiban kerahasiaan data pribadi?", 5)
		if e != nil || len(hits) != 1 || hits[0].BlobSHA256 != "a" || hits[0].ID != "S1" {
			t.Fatalf("ranking: %v %v", hits, e)
		}
	}
	hits, e := index.Search(context.Background(), "astronot mars", 5)
	if e != nil || len(hits) != 0 {
		t.Fatal("out-of-vocabulary query returned evidence")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = index.Search(ctx, "kerahasiaan", 5); e == nil {
		t.Fatal("cancel ignored")
	}
}
