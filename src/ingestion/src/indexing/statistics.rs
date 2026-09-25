//! Membekukan DF, panjang corpus dan parameter BM25 untuk vektor sparse generation.
//!
//! Dokumen diberi bobot TF/length, query diberi bobot qtf×IDF. Dot product keduanya
//! harus setara skor referensi `Bm25Statistics::score` pada generation yang sama.
//! Dictionary ID berasal dari allocator Go; fungsi ini tidak mengalokasikan ID,
//! memutasi statistik, ataupun menulis Qdrant. Input kosong/OOV dilaporkan eksplisit.
//! Revision dictionary descendant dari statistics-base yang terverifikasi boleh
//! menambah term baru dengan frozen DF=0. Encoder menolak mapping tanpa proof
//! ancestor statistics-base; admission record terhadap dictionary snapshot target
//! tetap batas Go. Provenance artifact/registry perlu dibuktikan sebelum publication.
//!
//! Benchmark: ukur waktu dua pass, bytes/record, peak RSS, parity skor dan retrieval
//! Recall@k terhadap configs/benchmark-targets.yaml; target REQUIRED_UNMEASURED.

use std::collections::BTreeMap;

use super::dictionary::LexicalDictionary;
use crate::wire::evidence::SparseVector;

const MAX_DOCUMENT_TOKENS: usize = 100_000;
const MAX_QUERY_TOKENS: usize = 1_024;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SparseWeightError {
    AnalyzerMismatch,
    DictionaryRevisionMismatch,
    EmptyCorpus,
    InvalidParameters,
    MissingDictionaryTerm,
    InvalidToken,
    TooManyTokens,
    InvalidWeight,
}

#[derive(Debug, Clone, PartialEq)]
pub struct FrozenBm25 {
    analyzer_id: String,
    dictionary_revision: String,
    dictionary_fingerprint: [u8; 32],
    document_count: u64,
    total_tokens: u64,
    df_by_id: BTreeMap<u32, u64>,
    k1: f64,
    b: f64,
}

impl FrozenBm25 {
    pub(crate) fn from_checked(
        analyzer_id: String,
        dictionary_revision: String,
        dictionary_fingerprint: [u8; 32],
        document_count: u64,
        total_tokens: u64,
        df_by_id: BTreeMap<u32, u64>,
        k1: f64,
        b: f64,
    ) -> Result<Self, SparseWeightError> {
        if document_count == 0 || total_tokens == 0 {
            return Err(SparseWeightError::EmptyCorpus);
        }
        if df_by_id.values().any(|df| *df == 0 || *df > document_count) {
            return Err(SparseWeightError::InvalidParameters);
        }
        Ok(Self {
            analyzer_id,
            dictionary_revision,
            dictionary_fingerprint,
            document_count,
            total_tokens,
            df_by_id,
            k1,
            b,
        })
    }

    fn check_dictionary(&self, dictionary: &LexicalDictionary) -> Result<(), SparseWeightError> {
        if dictionary.analyzer_id() != self.analyzer_id {
            return Err(SparseWeightError::AnalyzerMismatch);
        }
        if !dictionary.descends_from(&self.dictionary_revision, self.dictionary_fingerprint) {
            return Err(SparseWeightError::DictionaryRevisionMismatch);
        }
        Ok(())
    }

    /// Receives analyzed tokens from a document in the target snapshot. The caller
    /// proves target membership and binds this frozen statistics manifest before
    /// publishing; appended terms use DF=0 without changing old document weights.
    /// A term appended by a verified descendant is allowed with frozen DF=0.
    pub fn encode_document<'a>(
        &self,
        dictionary: &LexicalDictionary,
        tokens: impl IntoIterator<Item = &'a str>,
    ) -> Result<SparseVector, SparseWeightError> {
        self.check_dictionary(dictionary)?;
        let mut frequencies = BTreeMap::<u32, u32>::new();
        let mut length = 0usize;
        for term in tokens {
            if length == MAX_DOCUMENT_TOKENS {
                return Err(SparseWeightError::TooManyTokens);
            }
            length += 1;
            if term.is_empty() || term.len() > 256 || term.chars().any(char::is_control) {
                return Err(SparseWeightError::InvalidToken);
            }
            let id = dictionary
                .term_id(term)
                .ok_or(SparseWeightError::MissingDictionaryTerm)?;
            *frequencies.entry(id).or_insert(0) += 1;
        }
        let avg_len = self.total_tokens as f64 / self.document_count as f64;
        let norm = self.k1 * (1.0 - self.b + self.b * length as f64 / avg_len);
        let mut output = SparseVector::new();
        output.indices.reserve(frequencies.len());
        output.values.reserve(frequencies.len());
        for (id, tf) in frequencies {
            let weight = (tf as f64 * (self.k1 + 1.0) / (tf as f64 + norm)) as f32;
            if !weight.is_finite() || weight <= 0.0 {
                return Err(SparseWeightError::InvalidWeight);
            }
            output.indices.push(id);
            output.values.push(weight);
        }
        Ok(output)
    }

    /// Unknown query terms are counted as OOV, never assigned a fabricated ID.
    pub fn encode_query<'a>(
        &self,
        dictionary: &LexicalDictionary,
        tokens: impl IntoIterator<Item = &'a str>,
    ) -> Result<(SparseVector, u32), SparseWeightError> {
        self.check_dictionary(dictionary)?;
        let mut frequencies = BTreeMap::<u32, u32>::new();
        let mut oov = 0u32;
        let mut length = 0usize;
        for term in tokens {
            if length == MAX_QUERY_TOKENS {
                return Err(SparseWeightError::TooManyTokens);
            }
            length += 1;
            if term.is_empty() || term.len() > 256 || term.chars().any(char::is_control) {
                return Err(SparseWeightError::InvalidToken);
            }
            if let Some(id) = dictionary.term_id(term) {
                *frequencies.entry(id).or_insert(0) += 1;
            } else {
                oov += 1;
            }
        }
        let n = self.document_count as f64;
        let mut output = SparseVector::new();
        output.indices.reserve(frequencies.len());
        output.values.reserve(frequencies.len());
        for (id, qtf) in frequencies {
            let df = self.df_by_id.get(&id).copied().unwrap_or(0) as f64;
            let idf = (1.0 + (n - df + 0.5) / (df + 0.5)).ln();
            let weight = (qtf as f64 * idf) as f32;
            if !weight.is_finite() || weight <= 0.0 {
                return Err(SparseWeightError::InvalidWeight);
            }
            output.indices.push(id);
            output.values.push(weight);
        }
        Ok((output, oov))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::indexing::lexical::Bm25Statistics;

    fn dot(left: &SparseVector, right: &SparseVector) -> f64 {
        let mut result = 0.0;
        for (left_idx, left_weight) in left.indices.iter().zip(&left.values) {
            for (right_idx, right_weight) in right.indices.iter().zip(&right.values) {
                if left_idx == right_idx {
                    result += f64::from(*left_weight) * f64::from(*right_weight);
                }
            }
        }
        result
    }

    #[test]
    fn sparse_dot_matches_reference_after_incremental_change() {
        let mut stats = Bm25Statistics::new("a").unwrap();
        stats
            .upsert("a", "v1", ["pasal", "12/2020", "pasal"])
            .unwrap();
        stats.upsert("a", "v2", ["tidak", "wajib"]).unwrap();
        stats.remove("v2");
        stats
            .upsert("a", "v3", ["tidak", "wajib", "pasal"])
            .unwrap();
        let dictionary = LexicalDictionary::from_allocated_entries(
            "a",
            "r7",
            [
                ("pasal".into(), 8),
                ("12/2020".into(), 3),
                ("tidak".into(), 20),
                ("wajib".into(), 21),
            ],
        )
        .unwrap();
        let frozen = stats.freeze(&dictionary, 1.2, 0.75).unwrap();
        let (query, oov) = frozen
            .encode_query(&dictionary, ["pasal", "pasal", "12/2020", "unknown"])
            .unwrap();
        assert_eq!(oov, 1);
        assert_eq!(query.indices, vec![3, 8]);
        let doc = frozen
            .encode_document(&dictionary, ["pasal", "12/2020", "pasal"])
            .unwrap();
        let expected = stats
            .score(
                "a",
                "v1",
                ["pasal", "pasal", "12/2020", "unknown"],
                1.2,
                0.75,
            )
            .unwrap();
        assert!((dot(&doc, &query) - expected).abs() < 1e-6);
        assert_eq!(
            frozen
                .encode_query(&dictionary, ["other"])
                .unwrap()
                .0
                .indices
                .len(),
            0
        );
    }

    #[test]
    fn mismatched_dictionary_and_missing_term_are_rejected() {
        let mut stats = Bm25Statistics::new("a").unwrap();
        stats.upsert("a", "v1", ["pasal"]).unwrap();
        let incomplete =
            LexicalDictionary::from_allocated_entries("a", "r1", [("other".into(), 1)]).unwrap();
        assert_eq!(
            stats.freeze(&incomplete, 1.2, 0.75),
            Err(SparseWeightError::MissingDictionaryTerm)
        );
        let complete =
            LexicalDictionary::from_allocated_entries("a", "r1", [("pasal".into(), 1)]).unwrap();
        let frozen = stats.freeze(&complete, 1.2, 0.75).unwrap();
        let future =
            LexicalDictionary::from_allocated_entries("a", "r2", [("pasal".into(), 1)]).unwrap();
        assert_eq!(
            frozen.encode_query(&future, ["pasal"]),
            Err(SparseWeightError::DictionaryRevisionMismatch)
        );
        assert_eq!(
            frozen.encode_document(&complete, ["unknown"]),
            Err(SparseWeightError::MissingDictionaryTerm)
        );
    }

    #[test]
    fn appended_term_uses_frozen_df_zero_without_reweighting_old_terms() {
        let mut stats = Bm25Statistics::new("a").unwrap();
        stats.upsert("a", "v1", ["pasal", "pasal"]).unwrap();
        let base =
            LexicalDictionary::from_allocated_entries("a", "r1", [("pasal".into(), 7)]).unwrap();
        let frozen = stats.freeze(&base, 1.2, 0.75).unwrap();
        let child = LexicalDictionary::from_allocated_descendant(
            &base,
            "r2",
            [("pasal".into(), 7), ("izin".into(), 11)],
        )
        .unwrap();
        let (old_query, _) = frozen.encode_query(&base, ["pasal"]).unwrap();
        let (new_query, oov) = frozen.encode_query(&child, ["pasal", "izin"]).unwrap();
        assert_eq!(oov, 0);
        assert_eq!(new_query.indices, vec![7, 11]);
        assert_eq!(new_query.values[0], old_query.values[0]);
        assert!((f64::from(new_query.values[1]) - 4.0_f64.ln()).abs() < 1e-6);
        let doc = frozen.encode_document(&child, ["izin", "izin"]).unwrap();
        assert_eq!(doc.indices, vec![11]);
        assert!(dot(&doc, &new_query) > 0.0);
        assert_eq!(
            frozen.encode_document(&base, ["izin"]),
            Err(SparseWeightError::MissingDictionaryTerm)
        );
        let impostor =
            LexicalDictionary::from_allocated_entries("a", "r1", [("pasal".into(), 8)]).unwrap();
        assert_eq!(
            frozen.encode_query(&impostor, ["pasal"]),
            Err(SparseWeightError::DictionaryRevisionMismatch)
        );
    }
}
