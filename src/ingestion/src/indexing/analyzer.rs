//! Menganalisis teks chunk menjadi istilah BM25 memakai kebijakan lexical v1 yang terpin.
//!
//! Peran komponen: worker Rust menyiapkan token dokumen; query Go menerapkan aturan
//! identik dari fixture bersama sebelum menggunakan dictionary/statistics generasi sama.
//! Input UTF-8 yang terlalu besar, istilah panjang, atau jumlah token berlebih
//! ditolak; NFC stream-safe memakai properti Unicode 15 yang sama dengan query Go.
//! Kesalahan tidak menghasilkan indeks parsial. Aturan ini bukan tokenizer BGE-M3.
//!
//! Benchmark: ukur throughput teks/terms, peak RSS, dan dampak terhadap Recall@k serta
//! latency p50/p95/p99 pada configs/benchmark-targets.yaml. Target masih
//! REQUIRED_UNMEASURED; fixture parity tidak membuktikan kualitas retrieval.

use unicode_normalization::UnicodeNormalization;

use super::{letter_ranges, nfc_properties};

/// Perubahan normalisasi atau pemisahan token memerlukan generation/analyzer ID baru.
pub const ANALYZER_VERSION: &str = "regulagraph-lexical-nfc-ascii-v1";
const MAX_INPUT_BYTES: usize = 2_000_000;
const MAX_TERM_BYTES: usize = 256;
const MAX_TERMS: usize = 100_000;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AnalyzeError {
    InputTooLarge,
    TermTooLong,
    TooManyTerms,
}

/// NFC is applied before ASCII case folding. Only Unicode letters and ASCII digits
/// form terms; a hyphen remains between letters and a slash between ASCII digits.
/// Keeping legal identifiers in one term avoids conflating e.g. 12/2020 and 12/2021.
/// Non-ASCII case is intentionally preserved by v1 until measured corpus policy exists.
pub fn analyze_document(text: &str) -> Result<Vec<String>, AnalyzeError> {
    debug_assert_eq!(letter_ranges::LETTER_UNICODE_VERSION, "15.0.0");
    debug_assert_eq!(nfc_properties::NFC_UNICODE_VERSION, "15.0.0");
    if text.len() > MAX_INPUT_BYTES {
        return Err(AnalyzeError::InputTooLarge);
    }
    // x/text's stream-safe counter includes trailing Jamo from Hangul syllables.
    // Rust's built-in stream_safe differs there, so use the frozen x/text NFC
    // properties before composing. CGJ is a token boundary in both runtimes.
    let normalized: String = stream_safe_like_go(text).nfc().collect();
    let mut chars = normalized.chars().peekable();
    let mut terms = Vec::new();
    let mut current = String::new();
    let mut previous: Option<char> = None;
    while let Some(ch) = chars.next() {
        let next = chars.peek().copied();
        let keep_separator = matches!(
            (ch, previous, next),
            ('-', Some(left), Some(right)) if letter_ranges::is_letter(left) && letter_ranges::is_letter(right)
        ) || matches!(
            (ch, previous, next),
            ('/', Some(left), Some(right)) if left.is_ascii_digit() && right.is_ascii_digit()
        );
        if letter_ranges::is_letter(ch) || ch.is_ascii_digit() || keep_separator {
            current.push(if ch.is_ascii_uppercase() {
                ch.to_ascii_lowercase()
            } else {
                ch
            });
            if current.len() > MAX_TERM_BYTES {
                return Err(AnalyzeError::TermTooLong);
            }
            previous = Some(ch);
        } else {
            flush_term(&mut current, &mut terms)?;
            previous = None;
        }
    }
    flush_term(&mut current, &mut terms)?;
    Ok(terms)
}

fn stream_safe_like_go(text: &str) -> String {
    let mut output = String::with_capacity(text.len());
    let mut nonstarters = 0_u8;
    for ch in text.chars() {
        let (leading, trailing) = nfc_properties::leading_trailing(ch);
        if nonstarters + leading > 30 {
            output.push('\u{034F}');
            nonstarters = 0;
        }
        output.push(ch);
        if leading == 0 {
            nonstarters = trailing;
        } else {
            nonstarters += leading;
        }
    }
    output
}

fn flush_term(current: &mut String, terms: &mut Vec<String>) -> Result<(), AnalyzeError> {
    if current.is_empty() {
        return Ok(());
    }
    if terms.len() == MAX_TERMS {
        return Err(AnalyzeError::TooManyTerms);
    }
    terms.push(std::mem::take(current));
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;

    #[derive(Deserialize)]
    struct Fixture {
        version: String,
        cases: Vec<Case>,
    }
    #[derive(Deserialize)]
    struct Case {
        name: String,
        input: String,
        terms: Vec<String>,
    }

    #[test]
    fn shared_go_rust_cases() {
        assert_eq!(unicode_normalization::UNICODE_VERSION, (15, 0, 0));
        assert_eq!(letter_ranges::LETTER_UNICODE_VERSION, "15.0.0");
        assert_eq!(nfc_properties::NFC_UNICODE_VERSION, "15.0.0");
        let fixture: Fixture = serde_json::from_str(include_str!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../tests/fixtures/lexical-analyzer-v1.json"
        )))
        .unwrap();
        assert_eq!(fixture.version, ANALYZER_VERSION);
        for case in fixture.cases {
            assert_eq!(
                analyze_document(&case.input).unwrap(),
                case.terms,
                "{}",
                case.name
            );
        }
    }

    #[test]
    fn input_limits_fail_without_partial_terms() {
        assert_eq!(
            analyze_document(&"a".repeat(MAX_INPUT_BYTES + 1)),
            Err(AnalyzeError::InputTooLarge)
        );
        assert_eq!(
            analyze_document(&"a".repeat(MAX_TERM_BYTES + 1)),
            Err(AnalyzeError::TermTooLong)
        );
        assert_eq!(
            analyze_document(&"a ".repeat(MAX_TERMS + 1)),
            Err(AnalyzeError::TooManyTerms)
        );
    }

    #[test]
    fn long_hangul_modifier_sequence_keeps_go_stream_safe_boundary() {
        let modifier = "\u{FF9E}";
        let sample = format!("\u{AC00}{}", modifier.repeat(30));
        assert_eq!(
            analyze_document(&sample).unwrap(),
            vec![
                format!("\u{AC00}{}", modifier.repeat(29)),
                modifier.to_owned()
            ]
        );
    }

    #[test]
    fn decomposed_hangul_crossing_stream_safe_boundary() {
        let vowel = "\u{1161}";
        let sample = format!("\u{1100}{}", vowel.repeat(31));
        assert_eq!(
            analyze_document(&sample).unwrap(),
            vec![format!("\u{AC00}{}", vowel.repeat(29)), vowel.to_owned()]
        );
    }
}
