// Builds the local demo's immutable BM25 passage index from verified PDF pages.
// Query analysis reuses the production lexical tokenizer. Page windows retain
// exact UTF-8 offsets, and ranking is deterministic; no dense/graph search or
// temporal applicability is implied. Measure startup RSS and warm search latency
// separately; this sample is not benchmark-targets.yaml acceptance evidence.
package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/query"
)

type previewPosting struct {
	document int
	weight   float64
}
type PreviewIndex struct {
	passages []domain.PreviewPassage
	postings map[string][]previewPosting
	idf      map[string]float64
}

func NewPreviewIndex(pages []domain.PreviewPage) (*PreviewIndex, error) {
	index := &PreviewIndex{postings: map[string][]previewPosting{}, idf: map[string]float64{}}
	frequencies := []map[string]int{}
	lengths := []int{}
	total := 0
	for _, page := range pages {
		if !utf8.ValidString(page.Text) {
			return nil, errors.New("invalid page encoding")
		}
		// Windows end at paragraph/line boundaries when available. Oversized pages
		// remain bounded; offsets refer to exported page bytes, not PDF byte offsets.
		for start := 0; start < len(page.Text); {
			end := start
			count := 0
			for end < len(page.Text) && count < 1600 {
				_, n := utf8.DecodeRuneInString(page.Text[end:])
				end += n
				count++
			}
			if end < len(page.Text) {
				if split := strings.LastIndex(page.Text[start:end], "\n"); split > (end-start)/2 {
					end = start + split + 1
				}
			}
			text := page.Text[start:end]
			if len(strings.TrimSpace(text)) >= 60 {
				terms, err := query.AnalyzeLexicalQuery(page.Title + "\n" + text)
				if err != nil {
					return nil, err
				}
				if len(terms) > 0 {
					freq := map[string]int{}
					for _, term := range terms {
						freq[term]++
					}
					frequencies = append(frequencies, freq)
					lengths = append(lengths, len(terms))
					total += len(terms)
					index.passages = append(index.passages, domain.PreviewPassage{BlobSHA256: page.BlobSHA256, Title: page.Title, URL: page.URL, Page: page.Page, Text: text, StartByte: start, EndByte: end})
					if len(index.passages) > 25000 {
						return nil, errors.New("preview passage limit exceeded")
					}
				}
			}
			if end == len(page.Text) {
				break
			}
			// Small overlap retains sentence continuity but never loops on short windows.
			next := end
			for n := 0; n < 160 && next > start; n++ {
				_, size := utf8.DecodeLastRuneInString(page.Text[:next])
				next -= size
			}
			if next <= start {
				next = end
			}
			start = next
		}
	}
	if len(index.passages) == 0 {
		return nil, errors.New("no searchable text in preview corpus")
	}
	avg := float64(total) / float64(len(lengths))
	for i, freq := range frequencies {
		for term, tf := range freq {
			weight := float64(tf) * 2.2 / (float64(tf) + 1.2*(0.25+0.75*float64(lengths[i])/avg))
			index.postings[term] = append(index.postings[term], previewPosting{i, weight})
		}
	}
	for term, list := range index.postings {
		index.idf[term] = math.Log(1 + (float64(len(lengths)-len(list))+0.5)/(float64(len(list))+0.5))
	}
	return index, nil
}

func (p *PreviewIndex) PassageCount() int { return len(p.passages) }

func (p *PreviewIndex) Search(ctx context.Context, question string, limit int) ([]domain.PreviewPassage, error) {
	if limit < 1 || limit > 12 || len(question) > 2000 || strings.TrimSpace(question) == "" {
		return nil, errors.New("question or retrieval limit is invalid")
	}
	terms, err := query.AnalyzeLexicalQuery(question)
	if err != nil {
		return nil, err
	}
	stop := map[string]bool{}
	for _, s := range strings.Fields("apa apakah siapa bagaimana mengapa kapan dimana di mana yang dan atau untuk pada dalam adalah itu ini saya bisa tolong jelaskan sebutkan menurut tentang the a an is are what how of to in") {
		stop[s] = true
	}
	scores := map[int]float64{}
	seen := map[string]bool{}
	for _, term := range terms {
		if stop[term] || seen[term] {
			continue
		}
		seen[term] = true
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		for _, posting := range p.postings[term] {
			scores[posting.document] += p.idf[term] * posting.weight
		}
	}
	ids := make([]int, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if scores[a] != scores[b] {
			return scores[a] > scores[b]
		}
		return a < b
	})
	out := []domain.PreviewPassage{}
	pages := map[string]bool{}
	for _, id := range ids {
		hit := p.passages[id]
		key := fmt.Sprintf("%s:%d", hit.BlobSHA256, hit.Page)
		if pages[key] {
			continue
		}
		pages[key] = true
		hit.ID = fmt.Sprintf("S%d", len(out)+1)
		hit.Score = scores[id]
		out = append(out, hit)
		if len(out) == limit {
			break
		}
	}
	return out, ctx.Err()
}
