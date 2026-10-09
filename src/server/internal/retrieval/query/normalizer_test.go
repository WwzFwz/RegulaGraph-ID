// Checks query normalization against legal identifiers, negation, exact quotes,
// Unicode, byte-trace reconstruction and resource bounds. These are deterministic
// invariants, not evidence of retrieval recall or latency acceptance.
package query

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeQuestion(t *testing.T) {
	for _, tt := range []struct{ name, original, search string }{
		{"legal identifiers", " \tPasal 1\nayat (2) UU No. 11/2020 tidak berlaku?\u00a0", "Pasal 1 ayat (2) UU No. 11/2020 tidak berlaku?"},
		{"unicode", "Cafe\u0301\u2003izin", "Café izin"},
		{"literal terms", "  cari \"Cafe\u0301\t  11/2020\"  sekarang ", "cari \"Cafe\u0301\t  11/2020\" sekarang"},
		{"curly quotes", "  “Cafe\u0301  tidak”\n‘izin\t usaha’  ", "“Cafe\u0301  tidak” ‘izin\t usaha’"},
		{"single quotes", "  'izin\t  usaha'  bukan 'izin  kerja' ", "'izin\t  usaha' bukan 'izin  kerja'"},
		{"contractions", "don't\t  translate  user's  terms", "don't translate user's terms"},
		{"combining apostrophe boundary", "e\u0301'  x  '", "\u00e9' x '"},
		{"fuzz combining boundary", "000A\u0301'00\t0", "000\u00c1'00 0"},
		{"quoted combining apostrophe", "'e\u0301'word  x'", "'e\u0301'word  x'"},
		{"quoted contractions", " 'don't  change'\t‘don’t  change’ ", "'don't  change' ‘don’t  change’"},
		{"escaped quote", "  \"izin\\\"  usaha\"\t  tidak", "\"izin\\\"  usaha\" tidak"},
		{"unmatched quote", "  cari \"izin\t  usaha  ", "cari \"izin\t  usaha  "},
		{"no compatibility folding", "Ｐａｓａｌ ① １ vs 1; IV vs VI; m² != m2", "Ｐａｓａｌ ① １ vs 1; IV vs VI; m² != m2"},
		{"slang and mixed language", "ga boleh operate tanpa izin, isn't it?", "ga boleh operate tanpa izin, isn't it?"},
		{"typo preserved", "pasla 11 bukan pasal 1 sebelum 01/02/2020", "pasla 11 bukan pasal 1 sebelum 01/02/2020"},
		{"long combining sequence", "x" + strings.Repeat("\u0301", 70), "x" + strings.Repeat("\u0301", 70)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q, err := NormalizeQuestion(tt.original, MechanicalQuestion)
			if err != nil || q.Search != tt.search || q.Original != tt.original || q.Method != MechanicalQuestion {
				t.Fatalf("got %#v, %v; want %q", q, err, tt.search)
			}
			checkNormalizationTrace(t, q)
			again, err := NormalizeQuestion(q.Search, MechanicalQuestion)
			if err != nil || again.Search != q.Search || len(again.Edits) != 0 {
				t.Fatalf("not idempotent: %#v, %v", again, err)
			}
			original, err := NormalizeQuestion(tt.original, "")
			if err != nil || original.Method != OriginalQuestion || original.Search != tt.original || len(original.Edits) != 0 {
				t.Fatal("default changed original question", original, err)
			}
		})
	}
}

func checkNormalizationTrace(t testing.TB, q *NormalizedQuestion) {
	t.Helper()
	var rebuilt strings.Builder
	previousOriginal, previousSearch := 0, 0
	for _, edit := range q.Edits {
		if edit.OriginalStart < previousOriginal || edit.OriginalEnd <= edit.OriginalStart || edit.OriginalEnd > len(q.Original) ||
			edit.SearchStart < previousSearch || edit.SearchEnd < edit.SearchStart || edit.SearchEnd > len(q.Search) ||
			(edit.Kind != "whitespace" && edit.Kind != "nfc") {
			t.Fatal("invalid trace intervals", edit)
		}
		gap := q.Original[previousOriginal:edit.OriginalStart]
		if gap != q.Search[previousSearch:edit.SearchStart] || !utf8.ValidString(gap) ||
			!utf8.ValidString(q.Original[edit.OriginalStart:edit.OriginalEnd]) || !utf8.ValidString(q.Search[edit.SearchStart:edit.SearchEnd]) {
			t.Fatal("trace changed an unreported gap or split UTF-8", edit)
		}
		rebuilt.WriteString(gap)
		rebuilt.WriteString(q.Search[edit.SearchStart:edit.SearchEnd])
		previousOriginal, previousSearch = edit.OriginalEnd, edit.SearchEnd
	}
	rebuilt.WriteString(q.Original[previousOriginal:])
	if rebuilt.String() != q.Search {
		t.Fatal("trace does not reconstruct search question")
	}
}

func TestNormalizeQuestionBoundsAndOwnership(t *testing.T) {
	for _, input := range []string{"", " \t\u2003", "\xff", "a\x00b", strings.Repeat("x", MaximumQuestionBytes+1)} {
		for _, mode := range []NormalizationMode{OriginalQuestion, MechanicalQuestion} {
			if _, err := NormalizeQuestion(input, mode); err == nil {
				t.Fatal("invalid input accepted", mode)
			}
		}
	}
	if _, err := NormalizeQuestion("Pasal 1", "guess"); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if _, err := NormalizeQuestion(strings.Repeat("x", MaximumQuestionBytes), MechanicalQuestion); err != nil {
		t.Fatal("boundary input rejected", err)
	}
	// NFC can expand bytes (U+0344 decomposes); never truncate to fit the cap.
	if _, err := NormalizeQuestion(strings.Repeat("\u0344 ", MaximumQuestionBytes/3), MechanicalQuestion); err == nil {
		t.Fatal("output budget overflow accepted")
	}
	q, _ := NormalizeQuestion("  Pasal\t1 ", MechanicalQuestion)
	copy := q.Clone()
	copy.Edits[0].OriginalEnd++
	copy.Search = "changed"
	if q.Edits[0].OriginalEnd == copy.Edits[0].OriginalEnd || q.Search == copy.Search {
		t.Fatal("clone aliases caller-owned audit")
	}
}

func FuzzNormalizeQuestionTrace(f *testing.F) {
	for _, seed := range []string{"Pasal 11 bukan 1", " ‘don't  change’ ", "Cafe\u0301\tizin", "\xff", "\"unclosed  "} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		q, err := NormalizeQuestion(input, MechanicalQuestion)
		if err != nil {
			return
		}
		checkNormalizationTrace(t, q)
		if q.Original != input || len(q.Search) > MaximumQuestionBytes {
			t.Fatal("lost original or byte bound")
		}
		again, err := NormalizeQuestion(q.Search, MechanicalQuestion)
		if err != nil || again.Search != q.Search {
			t.Fatal("normalization not idempotent", err)
		}
	})
}

func BenchmarkNormalizeQuestion(b *testing.B) {
	input := strings.Repeat("  Cafe\u0301\tPasal 11 bukan 1; ‘izin  usaha’ ", 64)
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		if _, err := NormalizeQuestion(input, MechanicalQuestion); err != nil {
			b.Fatal(err)
		}
	}
}
