// Streaming PDF dan penyimpanan content-addressed untuk collector sumber.
// Peran: bytes PDF sungguhan disimpan bersama hash; HTML/error/unduhan terpotong ditolak.
// Integrasi: file sementara di direktori tujuan, validasi sebelum rename; receipt menunjuk path relatif.
// Performa: PDF tidak dimuat penuh ke RAM; batas ukuran, checksum reuse, dan waktu transfer terukur.
package sources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func verifyFile(path, expected string) bool {
	f, e := os.Open(path)
	if e != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == expected
}
func (c *Collector) validReceipt(r PDFReceipt) bool {
	return r.Error == "" && r.Bytes > 0 && validHash(r.SHA256) && r.Path == filepath.ToSlash(filepath.Join("blobs", r.SHA256+".pdf")) && verifyFile(filepath.Join(c.opts.OutputDir, filepath.FromSlash(r.Path)), r.SHA256)
}
func writeAtomic(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func (c *Collector) saveRecord(r Record) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	key := digest([]byte(r.SourceURL))
	// Historical observations are immutable; latest record is only a resume cursor.
	name := fmt.Sprintf("%d-%s.json", r.FetchedAt.UnixNano(), key)
	if err = writeAtomic(filepath.Join(c.opts.OutputDir, "observations", name), raw); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(c.opts.OutputDir, "records", key+".json"), raw)
}
func (c *Collector) readRecord(rawURL string) (Record, bool) {
	var r Record
	b, e := os.ReadFile(filepath.Join(c.opts.OutputDir, "records", digest([]byte(rawURL))+".json"))
	if e != nil || json.Unmarshal(b, &r) != nil || r.SchemaVersion != 1 || r.SourceURL != rawURL {
		return r, false
	}
	return r, true
}
func (c *Collector) download(ctx context.Context, link PDFLink) (receipt PDFReceipt) {
	start := time.Now()
	receipt = PDFReceipt{URL: link.URL, Label: link.Label, Kind: link.Kind, FetchedAt: start.UTC()}
	defer func() { receipt.DurationMS = time.Since(start).Milliseconds() }()
	resp, err := c.get(ctx, link.URL)
	if err != nil {
		receipt.Error = err.Error()
		return
	}
	defer resp.Body.Close()
	return c.savePDF(resp, resp.Body, link, start)
}
func (c *Collector) savePDF(resp *http.Response, body io.Reader, link PDFLink, start time.Time) (receipt PDFReceipt) {
	receipt = PDFReceipt{URL: link.URL, FinalURL: resp.Request.URL.String(), Label: link.Label, Kind: link.Kind,
		FetchedAt: start.UTC(), ContentType: resp.Header.Get("Content-Type"), ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
	defer func() { receipt.DurationMS = time.Since(start).Milliseconds() }()
	err := func() error {
		if resp.ContentLength > c.opts.MaxPDFBytes {
			return errors.New("PDF exceeds configured maximum size")
		}
		folder := filepath.Join(c.opts.OutputDir, "blobs")
		if e := os.MkdirAll(folder, 0755); e != nil {
			return e
		}
		f, e := os.CreateTemp(folder, ".pending-pdf-*")
		if e != nil {
			return e
		}
		temp := f.Name()
		defer os.Remove(temp)
		defer f.Close()
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, c.opts.MaxPDFBytes+1))
		if e != nil {
			return e
		}
		if n > c.opts.MaxPDFBytes {
			return errors.New("PDF exceeds configured maximum size")
		}
		head := make([]byte, 1024)
		if _, e = f.Seek(0, 0); e != nil {
			return e
		}
		read, _ := f.Read(head)
		if !bytes.HasPrefix(bytes.TrimSpace(head[:read]), []byte("%PDF-")) {
			return errors.New("response is not a PDF (missing PDF header)")
		}
		offset := n - 4096
		if offset < 0 {
			offset = 0
		}
		if _, e = f.Seek(offset, 0); e != nil {
			return e
		}
		tail, e := io.ReadAll(f)
		if e != nil {
			return e
		}
		if !bytes.Contains(tail, []byte("%%EOF")) {
			return errors.New("PDF appears incomplete (missing EOF marker)")
		}
		if e = f.Sync(); e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		sum := hex.EncodeToString(h.Sum(nil))
		dest := filepath.Join(folder, sum+".pdf")
		if !verifyFile(dest, sum) {
			if _, statErr := os.Stat(dest); statErr == nil {
				// Preserve corrupt bytes for diagnosis, then restore the verified download.
				quarantine := dest + fmt.Sprintf(".corrupt-%d", time.Now().UnixNano())
				if e = os.Rename(dest, quarantine); e != nil {
					return e
				}
			}
			if e = os.Rename(temp, dest); e != nil {
				if !verifyFile(dest, sum) {
					return e
				}
			}
		}
		receipt.SHA256 = sum
		receipt.Bytes = n
		receipt.Path = filepath.ToSlash(filepath.Join("blobs", sum+".pdf"))
		return nil
	}()
	if err != nil {
		receipt.Error = err.Error()
	}
	return
}
