//go:build ignore

// Generate the frozen Unicode Letter and NFC stream-safety property tables used
// by Rust BM25 indexing. Go query analysis uses unicode.IsLetter and x/text NFC.
// Both runtimes must use Unicode 15.0.0; upgrading it requires a new analyzer
// generation and cross-language parity review. The NFC table reads private
// x/text Properties fields offline, with explicit layout and version checks;
// production Rust and Go never use reflection. Run from repository root:
// go run scripts/generate_lexical_letters.go --write (or --check in verification).
// The O(Unicode scalar range) generation runs offline, never on a query path.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"reflect"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const letterOutputPath = "src/ingestion/src/indexing/letter_ranges.rs"
const normOutputPath = "src/ingestion/src/indexing/nfc_properties.rs"
const expectedUnicodeVersion = "15.0.0"

func main() {
	write := flag.Bool("write", false, "write the generated Rust table")
	check := flag.Bool("check", false, "check the committed Rust table")
	flag.Parse()
	if *write == *check {
		fail("choose exactly one of --write or --check")
	}
	if unicode.Version != expectedUnicodeVersion {
		fail("Go Unicode tables changed: " + unicode.Version)
	}
	if norm.Version != expectedUnicodeVersion {
		fail("x/text NFC Unicode tables changed: " + norm.Version)
	}
	verifyNormPropertiesLayout()
	letters, letterCount := renderLetterRanges()
	properties, propertyCount := renderNormProperties()
	verifyOutput(letterOutputPath, letters, *write)
	verifyOutput(normOutputPath, properties, *write)
	fmt.Printf("%s %d Unicode Letter ranges and %d NFC properties\n", map[bool]string{true: "wrote", false: "checked"}[*write], letterCount, propertyCount)
}

func renderLetterRanges() ([]byte, int) {
	var out bytes.Buffer
	fmt.Fprintln(&out, "//! Generated Unicode 15 Letter-category ranges for Rust lexical analyzer parity.")
	fmt.Fprintln(&out, "//!")
	fmt.Fprintln(&out, "//! Source: Go unicode.IsLetter, Unicode 15.0.0. Regenerate with")
	fmt.Fprintln(&out, "//! scripts/generate_lexical_letters.go. Binary-search lookup bounds per-term")
	fmt.Fprintln(&out, "//! classification cost; changing table requires a new analyzer generation.")
	fmt.Fprintln(&out, "//! Quality and latency targets: configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "pub const LETTER_UNICODE_VERSION: &str = \"15.0.0\";")
	fmt.Fprintln(&out, "const RANGES: &[(u32, u32)] = &[")
	inRange := false
	var start rune
	var count int
	for cp := rune(0); cp <= unicode.MaxRune+1; cp++ {
		letter := cp <= unicode.MaxRune && unicode.IsLetter(cp)
		if letter && !inRange {
			start = cp
			inRange = true
		}
		if !letter && inRange {
			fmt.Fprintf(&out, "    (0x%X, 0x%X),\n", start, cp-1)
			count++
			inRange = false
		}
	}
	fmt.Fprintln(&out, "];")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "pub fn is_letter(ch: char) -> bool {")
	fmt.Fprintln(&out, "    let codepoint = ch as u32;")
	fmt.Fprintln(&out, "    let index = RANGES.partition_point(|&(start, _)| start <= codepoint);")
	fmt.Fprintln(&out, "    index > 0 && codepoint <= RANGES[index - 1].1")
	fmt.Fprintln(&out, "}")
	return out.Bytes(), count
}

// The x/text stream-safe counter is based on two internal normalization
// properties, including Hangul's trailing Jamo count. No exported API exposes
// them; keep this reflection confined to a pinned offline code generator.
func verifyNormPropertiesLayout() {
	t := reflect.TypeOf(norm.NFC.PropertiesString("a"))
	expected := []string{"pos", "size", "ccc", "tccc", "nLead", "flags", "index"}
	if t.Kind() != reflect.Struct || t.NumField() != len(expected) {
		fail("x/text NFC Properties layout changed")
	}
	for i, name := range expected {
		field := t.Field(i)
		if field.Name != name || (i < len(expected)-1 && field.Type.Kind() != reflect.Uint8) || (i == len(expected)-1 && field.Type.Kind() != reflect.Uint16) {
			fail("x/text NFC Properties field layout changed")
		}
	}
}

func renderNormProperties() ([]byte, int) {
	var out bytes.Buffer
	fmt.Fprintln(&out, "//! Generated Unicode 15 x/text NFC stream-safety properties for Rust lexical parity.")
	fmt.Fprintln(&out, "//!")
	fmt.Fprintln(&out, "//! Source: pinned Go x/text norm.NFC.PropertiesString (nLead, flags & 3).")
	fmt.Fprintln(&out, "//! Regenerate with scripts/generate_lexical_letters.go; do not edit by hand.")
	fmt.Fprintln(&out, "//! Used only to match stream-safe NFC; normalization still uses unicode-normalization.")
	fmt.Fprintln(&out, "//! Quality and latency targets: configs/benchmark-targets.yaml (REQUIRED_UNMEASURED).")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "pub const NFC_UNICODE_VERSION: &str = \"15.0.0\";")
	fmt.Fprintln(&out, "const PROPERTIES: &[(u32, u32, u8, u8)] = &[")
	count := 0
	var first, last rune
	var previousLead, previousTrail uint8
	flush := func() {
		if first <= last {
			fmt.Fprintf(&out, "    (0x%X, 0x%X, %d, %d),\n", first, last, previousLead, previousTrail)
			count++
		}
	}
	first, last = 1, 0
	for cp := rune(0); cp <= unicode.MaxRune; cp++ {
		if !utf8.ValidRune(cp) {
			flush()
			first, last = 1, 0
			continue
		}
		props := reflect.ValueOf(norm.NFC.PropertiesString(string(cp)))
		lead := uint8(props.FieldByName("nLead").Uint())
		trail := uint8(props.FieldByName("flags").Uint() & 3)
		if lead == 0 && trail == 0 {
			flush()
			first, last = 1, 0
			continue
		}
		if first <= last && cp == last+1 && lead == previousLead && trail == previousTrail {
			last = cp
			continue
		}
		flush()
		first, last, previousLead, previousTrail = cp, cp, lead, trail
	}
	flush()
	fmt.Fprintln(&out, "];")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "pub fn leading_trailing(ch: char) -> (u8, u8) {")
	fmt.Fprintln(&out, "    let codepoint = ch as u32;")
	fmt.Fprintln(&out, "    let index = PROPERTIES.partition_point(|&(start, _, _, _)| start <= codepoint);")
	fmt.Fprintln(&out, "    if index > 0 && codepoint <= PROPERTIES[index - 1].1 {")
	fmt.Fprintln(&out, "        (PROPERTIES[index - 1].2, PROPERTIES[index - 1].3)")
	fmt.Fprintln(&out, "    } else {")
	fmt.Fprintln(&out, "        (0, 0)")
	fmt.Fprintln(&out, "    }")
	fmt.Fprintln(&out, "}")
	return out.Bytes(), count
}

func verifyOutput(path string, content []byte, write bool) {
	if write {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			fail(err.Error())
		}
		return
	}
	existing, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(existing, content) {
		fail("generated lexical table is stale: " + path + "; rerun with --write")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
