//! Membentuk representasi BM25 dan metadata indeks lexical dari chunk.
//!
//! Peran dalam komponen:
//! Menyiapkan pencarian istilah, nomor regulasi, dan rujukan spesifik.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Tokenisasi Indonesia dan tanda nomor harus diuji; sparse lexical BGE-M3 adalah representasi berbeda dari BM25.
//!
//! Benchmark dan gate penerimaan:
//! [INDEX] Ukur konsistensi ID/versi antarindeks, freshness lag, throughput indexing, ukuran indeks, dan retrieval Recall@k. Gate: penghapusan/upsert idempotent dan model/dimensi representasi cocok. Target kapasitas wajib mengikuti profil corpus/hardware dalam configs/benchmark-targets.yaml.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: statistik BM25 incremental untuk token hasil analyzer terpin aktif;
//! analyzer dokumen, artifact wire, writer indeks, dan query backend belum aktif.
//!
//! Batas runtime: worker hanya menyiapkan batch representasi dan metadata. Commit indeks dan publikasi snapshot dikoordinasikan Go; modul ini tidak menjadi pemilik publikasi kedua.
//! Rekomendasi implementasi berikutnya:
//! Bekukan analyzer Unicode/nomor hukum pada artifact berversi, serialisasikan statistik
//! deterministik, pin formula/qtf score, dan hubungkan generation yang sama ke writer dan query BM25.
//! Bukti verifikasi: Test legal identifiers, Unicode tokenization and incremental statistics; compare full rebuild parity and retrieval quality.
//! Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

use std::collections::BTreeMap;

const MAX_DOCUMENT_TOKENS: usize = 100_000;
const MAX_QUERY_TOKENS: usize = 1_024;
const MAX_TERM_BYTES: usize = 256;

/// Local, pre-analyzed statistics. The analyzer identity belongs to its immutable
/// artifact; tokenization policy is deliberately not duplicated in this module.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Bm25Statistics {
    analyzer_id: String,
    documents: BTreeMap<String, DocumentTerms>,
    document_frequency: BTreeMap<String, u64>,
    total_tokens: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct DocumentTerms {
    frequencies: BTreeMap<String, u32>,
    length: u32,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LexicalError {
    EmptyAnalyzer,
    AnalyzerMismatch,
    InvalidDocumentId,
    InvalidToken,
    TooManyTokens,
    UnknownDocument,
    InvalidParameters,
}

impl Bm25Statistics {
    pub fn new(analyzer_id: impl Into<String>) -> Result<Self, LexicalError> {
        let analyzer_id = analyzer_id.into();
        if analyzer_id.trim().is_empty() {
            return Err(LexicalError::EmptyAnalyzer);
        }
        Ok(Self {
            analyzer_id,
            documents: BTreeMap::new(),
            document_frequency: BTreeMap::new(),
            total_tokens: 0,
        })
    }

    pub fn analyzer_id(&self) -> &str {
        &self.analyzer_id
    }

    pub fn document_count(&self) -> usize {
        self.documents.len()
    }

    pub fn total_tokens(&self) -> u64 {
        self.total_tokens
    }

    pub fn document_frequency(&self, term: &str) -> u64 {
        self.document_frequency.get(term).copied().unwrap_or(0)
    }

    /// Replaces an exact versioned chunk ID. All validation precedes mutation, so
    /// malformed input cannot partially alter generation statistics. Calling this
    /// twice with the same ID/tokens is idempotent. Caller must rebuild on analyzer
    /// change and must keep source/provision/snapshot metadata in the IndexRecord.
    pub fn upsert<'a>(
        &mut self,
        analyzer_id: &str,
        document_id: &str,
        tokens: impl IntoIterator<Item = &'a str>,
    ) -> Result<(), LexicalError> {
        if analyzer_id != self.analyzer_id {
            return Err(LexicalError::AnalyzerMismatch);
        }
        if document_id.is_empty()
            || document_id.len() > 256
            || !document_id.bytes().all(|byte| (33..=126).contains(&byte))
        {
            return Err(LexicalError::InvalidDocumentId);
        }
        let mut next = DocumentTerms {
            frequencies: BTreeMap::new(),
            length: 0,
        };
        for token in tokens {
            if token.is_empty()
                || token.len() > MAX_TERM_BYTES
                || token.chars().any(char::is_control)
            {
                return Err(LexicalError::InvalidToken);
            }
            if next.length as usize == MAX_DOCUMENT_TOKENS {
                return Err(LexicalError::TooManyTokens);
            }
            next.length += 1;
            *next.frequencies.entry(token.to_owned()).or_insert(0) += 1;
        }
        let prior_length = self
            .documents
            .get(document_id)
            .map_or(0, |d| u64::from(d.length));
        if self.total_tokens - prior_length > u64::MAX - u64::from(next.length) {
            return Err(LexicalError::TooManyTokens);
        }
        self.remove(document_id);
        self.total_tokens += u64::from(next.length);
        for term in next.frequencies.keys() {
            *self.document_frequency.entry(term.clone()).or_insert(0) += 1;
        }
        self.documents.insert(document_id.to_owned(), next);
        Ok(())
    }

    /// Removes one versioned document; repeated deletion is a safe no-op.
    pub fn remove(&mut self, document_id: &str) -> bool {
        let Some(prior) = self.documents.remove(document_id) else {
            return false;
        };
        self.total_tokens -= u64::from(prior.length);
        for term in prior.frequencies.keys() {
            if let Some(frequency) = self.document_frequency.get_mut(term) {
                *frequency -= 1;
                if *frequency == 0 {
                    self.document_frequency.remove(term);
                }
            }
        }
        true
    }

    /// Scores an already analyzed query against one document in this generation.
    /// No retrieval result is emitted here: visibility and legal-version filtering
    /// belong to the snapshot-pinned query backend before fusion.
    pub fn score<'a>(
        &self,
        analyzer_id: &str,
        document_id: &str,
        query_tokens: impl IntoIterator<Item = &'a str>,
        k1: f64,
        b: f64,
    ) -> Result<f64, LexicalError> {
        if analyzer_id != self.analyzer_id {
            return Err(LexicalError::AnalyzerMismatch);
        }
        if !k1.is_finite() || k1 <= 0.0 || !b.is_finite() || !(0.0..=1.0).contains(&b) {
            return Err(LexicalError::InvalidParameters);
        }
        let document = self
            .documents
            .get(document_id)
            .ok_or(LexicalError::UnknownDocument)?;
        let mut query_frequency: BTreeMap<&str, u32> = BTreeMap::new();
        let mut query_length = 0;
        for term in query_tokens {
            if query_length == MAX_QUERY_TOKENS {
                return Err(LexicalError::TooManyTokens);
            }
            query_length += 1;
            if term.is_empty() || term.len() > MAX_TERM_BYTES || term.chars().any(char::is_control)
            {
                return Err(LexicalError::InvalidToken);
            }
            let entry = query_frequency.entry(term).or_insert(0);
            *entry = entry.saturating_add(1);
        }
        if self.documents.is_empty() || self.total_tokens == 0 {
            return Ok(0.0);
        }
        let n = self.documents.len() as f64;
        let average_length = self.total_tokens as f64 / n;
        let normalization = k1 * (1.0 - b + b * f64::from(document.length) / average_length);
        if !normalization.is_finite() {
            return Err(LexicalError::InvalidParameters);
        }
        let mut score = 0.0;
        for (term, query_count) in query_frequency {
            let Some(tf) = document.frequencies.get(term) else {
                continue;
            };
            let df = self.document_frequency(term) as f64;
            let idf = (1.0 + (n - df + 0.5) / (df + 0.5)).ln();
            score += f64::from(query_count) * idf * f64::from(*tf) * (k1 + 1.0)
                / (f64::from(*tf) + normalization);
            if !score.is_finite() {
                return Err(LexicalError::InvalidParameters);
            }
        }
        Ok(score)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn incremental_replacement_matches_full_rebuild() {
        let mut incremental = Bm25Statistics::new("analyzer:sha256:a").unwrap();
        incremental
            .upsert(
                "analyzer:sha256:a",
                "chunk:1:v1",
                ["pasal", "12", "ayat", "1"],
            )
            .unwrap();
        incremental
            .upsert(
                "analyzer:sha256:a",
                "chunk:2:v1",
                ["pasal", "13", "ayat", "2"],
            )
            .unwrap();
        incremental
            .upsert(
                "analyzer:sha256:a",
                "chunk:1:v1",
                ["pasal", "12", "ayat", "2", "berlaku"],
            )
            .unwrap();
        incremental.remove("chunk:2:v1");
        incremental
            .upsert("analyzer:sha256:a", "chunk:3:v1", ["izin", "berlaku"])
            .unwrap();

        let mut rebuilt = Bm25Statistics::new("analyzer:sha256:a").unwrap();
        rebuilt
            .upsert("analyzer:sha256:a", "chunk:3:v1", ["izin", "berlaku"])
            .unwrap();
        rebuilt
            .upsert(
                "analyzer:sha256:a",
                "chunk:1:v1",
                ["pasal", "12", "ayat", "2", "berlaku"],
            )
            .unwrap();
        assert_eq!(incremental, rebuilt);
        assert_eq!(rebuilt.document_frequency("berlaku"), 2);
        assert_eq!(rebuilt.document_frequency("13"), 0);
        assert!(
            rebuilt
                .score(
                    "analyzer:sha256:a",
                    "chunk:1:v1",
                    ["pasal", "12"],
                    1.2,
                    0.75
                )
                .unwrap()
                > rebuilt
                    .score(
                        "analyzer:sha256:a",
                        "chunk:3:v1",
                        ["pasal", "12"],
                        1.2,
                        0.75
                    )
                    .unwrap()
        );
    }

    #[test]
    fn rejected_update_cannot_change_statistics() {
        let mut stats = Bm25Statistics::new("analyzer:a").unwrap();
        stats
            .upsert(
                "analyzer:a",
                "chunk:1",
                ["undang-undang", "2024", "kewajiban"],
            )
            .unwrap();
        let before = stats.clone();
        assert_eq!(
            stats.upsert("analyzer:b", "chunk:1", ["lain"]),
            Err(LexicalError::AnalyzerMismatch)
        );
        assert_eq!(
            stats.upsert("analyzer:a", "chunk:1", ["valid", ""]),
            Err(LexicalError::InvalidToken)
        );
        assert_eq!(stats, before);
        assert_eq!(
            stats.upsert("analyzer:a", "chunk 2", ["valid"]),
            Err(LexicalError::InvalidDocumentId)
        );
        assert_eq!(
            stats.upsert("analyzer:a", "chunk\n2", ["valid"]),
            Err(LexicalError::InvalidDocumentId)
        );
        stats
            .upsert(
                "analyzer:a",
                "chunk:1",
                ["undang-undang", "2024", "kewajiban"],
            )
            .unwrap();
        assert_eq!(stats, before);
        assert_eq!(
            stats.score("analyzer:b", "chunk:1", ["2024"], 1.2, 0.75),
            Err(LexicalError::AnalyzerMismatch)
        );
        assert_eq!(
            stats.score("analyzer:a", "chunk:1", ["2024"], f64::NAN, 0.75),
            Err(LexicalError::InvalidParameters)
        );
        assert_eq!(
            stats.score(
                "analyzer:a",
                "chunk:1",
                std::iter::repeat("2024").take(MAX_QUERY_TOKENS + 1),
                1.2,
                0.75
            ),
            Err(LexicalError::TooManyTokens)
        );
        stats.upsert("analyzer:a", "chunk:2", ["singkat"]).unwrap();
        assert_eq!(
            stats.score("analyzer:a", "chunk:1", ["2024"], f64::MAX, 1.0),
            Err(LexicalError::InvalidParameters)
        );
    }

    #[test]
    fn unicode_terms_are_preserved_as_analyzer_output() {
        let mut stats = Bm25Statistics::new("analyzer:unicode").unwrap();
        stats
            .upsert(
                "analyzer:unicode",
                "chunk:é",
                ["perizinan", "syari’ah", "2026"],
            )
            .unwrap_err();
        stats
            .upsert(
                "analyzer:unicode",
                "chunk:1",
                ["perizinan", "syari’ah", "2026"],
            )
            .unwrap();
        assert_eq!(stats.document_frequency("syari’ah"), 1);
    }
}
