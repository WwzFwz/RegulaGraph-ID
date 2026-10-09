// Menghasilkan representasi pencarian pertanyaan sambil mempertahankan versi aslinya.
//
// Peran dalam komponen:
// Menyiapkan bentuk pencarian mekanis; typo, bahasa informal, dan code-switch
// dipertahankan tanpa koreksi atau interpretasi otomatis.
//
// Kontrak integrasi dan perhatian implementasi:
// Jangan mengubah nomor/tahun, negasi, atau filter waktu; normalisasi opsional dievaluasi per kategori pertanyaan.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: normalisasi mekanis opt-in aktif dengan trace byte UTF-8; original-v1
// mempertahankan seluruh input. Tidak mengoreksi typo, menerjemahkan, melakukan
// case/compatibility folding atau menebak tanggal. Graph/reranker/generator memakai
// pertanyaan asli. Kompleksitas linear dengan input/output maksimal 64 KiB.
// Perubahan aturan membutuhkan versi policy baru dan evaluasi kualitas tersendiri.
// Bukti verifikasi: Test informal/typo/Indonesian-English cases and destructive normalization counterexamples; record original-to-normalized trace.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package query

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// NormalizationMode is a versioned operator choice, not an inferred query intent.
type NormalizationMode string

const (
	OriginalQuestion     NormalizationMode = "original-v1"
	MechanicalQuestion   NormalizationMode = "mechanical-v1"
	MaximumQuestionBytes                   = 64 << 10
)

// NormalizationEdit maps changed intervals only, in UTF-8 bytes [start,end).
// Unchanged gaps are copied exactly. Empty search intervals represent trimming.
type NormalizationEdit struct {
	OriginalStart int    `json:"original_start"`
	OriginalEnd   int    `json:"original_end"`
	SearchStart   int    `json:"search_start"`
	SearchEnd     int    `json:"search_end"`
	Kind          string `json:"kind"`
}

type NormalizedQuestion struct {
	Method   NormalizationMode   `json:"method"`
	Original string              `json:"original"`
	Search   string              `json:"search"`
	Edits    []NormalizationEdit `json:"edits,omitempty"`
}

func (q *NormalizedQuestion) Clone() *NormalizedQuestion {
	if q == nil {
		return nil
	}
	copy := *q
	copy.Edits = append([]NormalizationEdit(nil), q.Edits...)
	return &copy
}

func ParseNormalizationMode(value string) (NormalizationMode, error) {
	switch NormalizationMode(value) {
	case "", OriginalQuestion:
		return OriginalQuestion, nil
	case MechanicalQuestion:
		return MechanicalQuestion, nil
	default:
		return "", errors.New("query normalization must be original-v1 or mechanical-v1")
	}
}

// NormalizeQuestion applies only canonical NFC and outside-quote whitespace
// normalization. Quoted spans (including an unmatched opener's remainder) are
// byte-preserved. No case folding, NFKC, stemming, spelling or lexical expansion
// occurs here. Quote protection is conservative, not a natural-language parser.
// All work is local and bounded; no I/O, model calls or caller-owned mutation.
func NormalizeQuestion(input string, mode NormalizationMode) (*NormalizedQuestion, error) {
	selected, err := ParseNormalizationMode(string(mode))
	if err != nil {
		return nil, err
	}
	if len(input) == 0 || len(input) > MaximumQuestionBytes || !utf8.ValidString(input) || strings.ContainsRune(input, 0) || strings.TrimSpace(input) == "" {
		return nil, errors.New("nonblank valid UTF-8 question within 64 KiB required")
	}
	result := &NormalizedQuestion{Method: selected, Original: input, Search: input}
	if selected == OriginalQuestion {
		return result, nil
	}
	var out strings.Builder
	out.Grow(len(input))
	appendPart := func(start, end int, value, kind string) error {
		if len(value) > MaximumQuestionBytes-out.Len() {
			return errors.New("normalized question exceeds 64 KiB")
		}
		before := out.Len()
		out.WriteString(value)
		if value != input[start:end] {
			result.Edits = append(result.Edits, NormalizationEdit{OriginalStart: start, OriginalEnd: end, SearchStart: before, SearchEnd: out.Len(), Kind: kind})
		}
		return nil
	}
	for i := 0; i < len(input); {
		start := i
		r, width := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(r) {
			i += width
			for i < len(input) {
				r, width = utf8.DecodeRuneInString(input[i:])
				if !unicode.IsSpace(r) {
					break
				}
				i += width
			}
			replacement := " "
			if out.Len() == 0 || i == len(input) {
				replacement = ""
			}
			if err = appendPart(start, i, replacement, "whitespace"); err != nil {
				return nil, err
			}
			continue
		}
		if close := questionQuote(input, i); close != 0 {
			i += width
			for i < len(input) {
				r, width = utf8.DecodeRuneInString(input[i:])
				i += width
				if r == '\\' && i < len(input) {
					_, width = utf8.DecodeRuneInString(input[i:])
					i += width
					continue
				}
				if r == close && !((r == 0x27 || r == 0x2019) && questionApostrophe(input, i-width, i)) {
					break
				}
			}
			if err = appendPart(start, i, input[start:i], ""); err != nil {
				return nil, err
			}
			continue
		}
		i += width
		for i < len(input) {
			r, width = utf8.DecodeRuneInString(input[i:])
			if unicode.IsSpace(r) || questionQuote(input, i) != 0 {
				break
			}
			i += width
		}
		normalized := norm.NFC.String(input[start:i])
		// x/text inserts CGJ for stream-safe long combining sequences. Preserve such
		// tokens instead of silently introducing a code point into the user's query.
		if strings.Count(normalized, "\u034f") != strings.Count(input[start:i], "\u034f") {
			normalized = input[start:i]
		}
		if err = appendPart(start, i, normalized, "nfc"); err != nil {
			return nil, err
		}
	}
	result.Search = out.String()
	return result, nil
}

func questionQuote(input string, at int) rune {
	r, _ := utf8.DecodeRuneInString(input[at:])
	switch r {
	case '"':
		return '"'
	case 0x201c:
		return 0x201d
	case 0x2018:
		return 0x2019
	case 0x27:
		// A straight apostrophe inside a word is not an opening quotation.
		if at > 0 {
			previous, _ := utf8.DecodeLastRuneInString(input[:at])
			if questionWordRune(previous) {
				return 0
			}
		}
		return 0x27
	}
	return 0
}

func questionApostrophe(input string, start, end int) bool {
	if start == 0 || end == len(input) {
		return false
	}
	before, _ := utf8.DecodeLastRuneInString(input[:start])
	after, _ := utf8.DecodeRuneInString(input[end:])
	return questionWordRune(before) && questionWordRune(after)
}

// Combining marks remain part of a word before and after NFC composition.
// Ignoring them would change quotation boundaries on repeated normalization.
func questionWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}
