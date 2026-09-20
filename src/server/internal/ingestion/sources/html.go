// Parser metadata detail/listing BPK dan Kemkomdigi serta tautan PDF eksplisit.
// Peran: membaca metadata portal, tanpa menafsirkan tanggal/status hukum atau isi PDF.
// Integrasi: metadata mentah dan PDF terkait dipisah; body dibatasi oleh fetcher.
// Performa: traversal DOM untuk akuisisi offline; perubahan layout diuji dengan fixture.
package sources

import (
	"bytes"
	"fmt"
	"golang.org/x/net/html"
	"net/url"
	"strconv"
	"strings"
)

var fieldLabels = map[string]string{
	"judul": "title", "tipe dokumen": "document_type", "nomor": "number", "tahun": "year",
	"bentuk": "regulation_type", "bentuk singkat": "regulation_abbreviation", "t.e.u.": "issuer",
	"tanggal penetapan": "enactment_date", "tanggal pengundangan": "promulgation_date",
	"tanggal berlaku": "effective_date", "tempat penetapan": "enactment_place",
	"sumber": "publication_source", "bahasa": "language", "status": "portal_status", "lokasi": "location",
}

const MetadataParserVersion = "portal-html-v1.1"

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style") {
			return
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			b.WriteByte(' ')
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
func attribute(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
func nextElement(n *html.Node) *html.Node {
	for n = n.NextSibling; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode {
			return n
		}
	}
	return nil
}

func ParsePage(raw []byte, location string) (Page, error) {
	base, err := url.Parse(location)
	if err != nil {
		return Page{}, err
	}
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return Page{}, fmt.Errorf("parse HTML: %w", err)
	}
	p := Page{Fields: map[string][]string{}, DocumentTitles: map[string]string{}}
	seenLinks := map[string]bool{}
	seenDocs := map[string]bool{}
	seenNext := map[string]bool{}
	resolve := func(s string) string {
		u, e := url.Parse(strings.TrimSpace(s))
		if e != nil || s == "" {
			return ""
		}
		u = base.ResolveReference(u)
		u.Fragment = ""
		if u.Scheme != "https" && u.Scheme != "http" {
			return ""
		}
		return u.String()
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.ElementNode {
			if n.Data == "h1" && p.Title == "" {
				p.Title = nodeText(n)
			}
			if n.Data == "div" || n.Data == "td" || n.Data == "dt" || n.Data == "label" {
				var leading strings.Builder
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type != html.TextNode {
						break
					}
					leading.WriteString(c.Data)
				}
				label := strings.Trim(strings.ToLower(strings.Join(strings.Fields(leading.String()), " ")), " :")
				if key, ok := fieldLabels[label]; ok {
					value := ""
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && c.Data != "br" {
							value = nodeText(c)
							if value != "" {
								break
							}
						}
					}
					if value == "" {
						if sibling := nextElement(n); sibling != nil {
							value = nodeText(sibling)
						}
					}
					if value != "" && len(value) < 8192 {
						duplicate := false
						for _, v := range p.Fields[key] {
							if v == value {
								duplicate = true
							}
						}
						if !duplicate {
							p.Fields[key] = append(p.Fields[key], value)
						}
					}
				}
			}
			if n.Data == "a" || n.Data == "iframe" || n.Data == "embed" || n.Data == "object" {
				rawURL := attribute(n, "href")
				if rawURL == "" {
					rawURL = attribute(n, "src")
				}
				if rawURL == "" {
					rawURL = attribute(n, "data")
				}
				resolved := resolve(rawURL)
				if resolved != "" {
					u, _ := url.Parse(resolved)
					path := strings.ToLower(u.Path)
					label := nodeText(n)
					if embedded := u.Query().Get("file"); embedded != "" && strings.Contains(strings.ToLower(embedded), ".pdf") {
						if target := resolve(embedded); target != "" {
							resolved = target
							u, _ = url.Parse(resolved)
							path = strings.ToLower(u.Path)
						}
					}
					isPDF := strings.HasSuffix(path, ".pdf") || strings.Contains(path, "/download/") || strings.Contains(path, "/unduh/")
					if strings.EqualFold(base.Hostname(), "peraturan.bpk.go.id") && strings.HasPrefix(path, "/read/") {
						u.Path = "/Download/" + u.Path[len("/Read/"):]
						u.RawPath = ""
						resolved = u.String()
						path = strings.ToLower(u.Path)
					}
					if isPDF && !seenLinks[resolved] {
						seenLinks[resolved] = true
						kind := "document"
						if strings.Contains(path, "downloadujimateri") {
							kind = "related_judgment"
						}
						link := PDFLink{URL: resolved, Label: label, Kind: kind}
						if kind == "related_judgment" {
							p.RelatedPDFs = append(p.RelatedPDFs, link)
						} else {
							p.PDFs = append(p.PDFs, link)
						}
					}
					jdihnDocument := (base.Hostname() == "jdihn.go.id" || base.Hostname() == "www.jdihn.go.id") && strings.HasPrefix(path, "/doc/")
					if n.Data == "a" && (strings.Contains(path, "/details/") || strings.Contains(path, "/produk_hukum/view/id/") || jdihnDocument) && !seenDocs[resolved] {
						seenDocs[resolved] = true
						p.DocumentURLs = append(p.DocumentURLs, resolved)
						p.DocumentTitles[resolved] = label
					}
					next := strings.Contains(strings.ToLower(attribute(n, "rel")), "next") || strings.EqualFold(label, "next") || strings.EqualFold(label, "selanjutnya") || strings.EqualFold(attribute(n, "aria-label"), "Next")
					// BPK's label Next jumps ten pages. Follow the actual adjacent page instead.
					if base.Hostname() == "peraturan.bpk.go.id" && strings.EqualFold(base.Path, "/Search") {
						current, _ := strconv.Atoi(base.Query().Get("p"))
						if current < 1 {
							current = 1
						}
						candidate, _ := strconv.Atoi(u.Query().Get("p"))
						bq, uq := base.Query(), u.Query()
						bq.Del("p")
						uq.Del("p")
						next = strings.EqualFold(u.Path, base.Path) && candidate == current+1 && bq.Encode() == uq.Encode()
					}
					if n.Data == "a" && next && u.Hostname() == base.Hostname() && !seenNext[resolved] {
						seenNext[resolved] = true
						p.NextURLs = append(p.NextURLs, resolved)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if p.Title == "" && len(p.Fields["title"]) > 0 {
		p.Title = p.Fields["title"][0]
	}
	return p, nil
}
