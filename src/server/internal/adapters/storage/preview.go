// Loads an immutable local-demo page export with manifest, byte and path checks.
// Original D01 records supply titles/URLs; they never assert canonical identity.
// SHA256SUMS pins every input before retrieval builds its in-memory index. Files
// are opened through os.Root; total/page/item bounds protect startup RSS. This is
// an offline sample, not a published PostgreSQL snapshot or production evidence.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/ingestion/sources"
)

type PreviewCorpus struct {
	Pages       []domain.PreviewPage
	PDFs        map[string]sources.PDFReceipt
	Fingerprint string
}

func LoadPreviewCorpus(directory string) (*PreviewCorpus, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	read := func(name string, maximum int64) ([]byte, error) {
		f, e := root.Open(name)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
		if e != nil {
			return nil, e
		}
		if int64(len(raw)) > maximum {
			return nil, errors.New("preview file exceeds byte budget")
		}
		return raw, nil
	}
	manifest, err := read("SHA256SUMS", 4<<20)
	if err != nil {
		return nil, err
	}
	entries := strings.Split(strings.TrimSpace(string(manifest)), "\n")
	if len(entries) > 20000 {
		return nil, errors.New("too many preview artifacts")
	}
	pattern := regexp.MustCompile(`^[a-f0-9]{64}/(record\.json|page-[0-9]{5}\.txt)$`)
	files := map[string][]byte{}
	total := 0
	for _, line := range entries {
		parts := strings.SplitN(strings.TrimSpace(line), "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 || (!pattern.MatchString(parts[1]) && parts[1] != "report.json") {
			return nil, errors.New("invalid preview manifest entry")
		}
		if _, exists := files[parts[1]]; exists {
			return nil, errors.New("duplicate preview manifest entry")
		}
		raw, e := read(parts[1], 1<<20)
		if e != nil {
			return nil, e
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != parts[0] || !utf8.Valid(raw) {
			return nil, errors.New("preview artifact hash/UTF-8 mismatch")
		}
		total += len(raw)
		if total > 64<<20 {
			return nil, errors.New("preview corpus exceeds 64 MiB")
		}
		files[parts[1]] = raw
	}
	sum := sha256.Sum256(manifest)
	corpus := &PreviewCorpus{PDFs: map[string]sources.PDFReceipt{}, Fingerprint: hex.EncodeToString(sum[:])}
	records := map[string]sources.Record{}
	for name, raw := range files {
		if !strings.HasSuffix(name, "/record.json") {
			continue
		}
		var r sources.Record
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		blob := strings.Split(name, "/")[0]
		if r.SchemaVersion != 1 || r.Status != "complete" || r.Metadata.Title == "" {
			return nil, errors.New("invalid demo source record")
		}
		u, e := url.Parse(r.SourceURL)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, errors.New("invalid source URL")
		}
		for _, p := range r.PDFs {
			if p.SHA256 == blob && p.Kind == "document" && p.Bytes > 0 && p.Bytes <= 50<<20 {
				corpus.PDFs[blob] = p
			}
		}
		if _, ok := corpus.PDFs[blob]; !ok {
			return nil, errors.New("source PDF receipt missing")
		}
		records[blob] = r
	}
	for name, raw := range files {
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		blob := strings.Split(name, "/")[0]
		r, ok := records[blob]
		if !ok {
			return nil, errors.New("page has no source record")
		}
		var page int
		page, err = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(strings.Split(name, "/")[1], "page-"), ".txt"))
		if err != nil || page < 1 {
			return nil, errors.New("invalid page number")
		}
		corpus.Pages = append(corpus.Pages, domain.PreviewPage{BlobSHA256: blob, Title: r.Metadata.Title, URL: r.SourceURL, Page: page, Text: string(raw)})
	}
	if len(corpus.Pages) == 0 {
		return nil, errors.New("preview corpus has no pages")
	}
	sort.Slice(corpus.Pages, func(i, j int) bool {
		a, b := corpus.Pages[i], corpus.Pages[j]
		return fmt.Sprintf("%s:%05d", a.BlobSHA256, a.Page) < fmt.Sprintf("%s:%05d", b.BlobSHA256, b.Page)
	})
	return corpus, nil
}
