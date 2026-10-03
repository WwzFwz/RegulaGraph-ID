//! Checks frozen BM25 wire artifacts before worker document weighting. The exact
//! base dictionary, corpus, formula and population counts must agree; descendant
//! reuse is subsequently checked by FrozenBm25. Byte hashes, registry authority
//! and snapshot membership remain caller obligations. Decode once per generation
//! with explicit budgets; benchmark RSS/load time and score parity separately
//! from retrieval quality under configs/benchmark-targets.yaml (unmeasured).

use super::{
    analyzer::ANALYZER_VERSION, dictionary_artifact::CheckedDictionary, statistics::FrozenBm25,
};
use crate::{
    domain::wire::{self, Limits},
    wire::evidence::{LexicalAnalyzerArtifact, LexicalStatisticsArtifact},
};
use protobuf::MessageFull;
use std::collections::{BTreeMap, BTreeSet};

pub const FORMULA_V1: &str = "regulagraph-frozen-bm25-v1";

/// Emits exactly the analyzer implemented by this worker, with owned metadata.
pub fn build_analyzer(
    meta: &crate::wire::common::RecordMeta,
    limits: Limits,
) -> Result<LexicalAnalyzerArtifact, String> {
    let a = LexicalAnalyzerArtifact {
        meta: protobuf::MessageField::some(meta.clone()),
        analyzer_id: ANALYZER_VERSION.into(),
        unicode_version: "15.0.0".into(),
        maximum_input_bytes: 2_000_000,
        maximum_term_bytes: 256,
        maximum_document_terms: 100_000,
        maximum_query_terms: 1_024,
        ..Default::default()
    };
    check_analyzer(&a, &meta.corpus_id, limits)?;
    Ok(a)
}

pub fn check_analyzer(
    a: &LexicalAnalyzerArtifact,
    corpus: &str,
    limits: Limits,
) -> Result<(), String> {
    wire::validate(a, limits)?;
    if unicode_normalization::UNICODE_VERSION != (15, 0, 0)
        || super::letter_ranges::LETTER_UNICODE_VERSION != "15.0.0"
        || super::nfc_properties::NFC_UNICODE_VERSION != "15.0.0"
    {
        return Err("runtime Unicode tables differ from pinned analyzer".into());
    }
    if a.meta.corpus_id != corpus
        || a.meta.visibility.is_some()
        || a.analyzer_id != ANALYZER_VERSION
        || a.unicode_version != "15.0.0"
        || a.maximum_input_bytes != 2_000_000
        || a.maximum_term_bytes != 256
        || a.maximum_document_terms != 100_000
        || a.maximum_query_terms != 1_024
    {
        return Err("unsupported lexical analyzer policy or corpus".into());
    }
    Ok(())
}

/// Decode bytes already authenticated against the generation ArtifactRef.
pub fn decode_statistics(
    raw: &[u8],
    base: &CheckedDictionary,
    limits: Limits,
) -> Result<FrozenBm25, String> {
    let decoded = wire::decode(raw, &LexicalStatisticsArtifact::descriptor(), limits)?;
    check_statistics(
        decoded
            .downcast_ref::<LexicalStatisticsArtifact>()
            .ok_or("statistics descriptor mismatch")?,
        base,
        limits,
    )
}

pub fn check_statistics(
    a: &LexicalStatisticsArtifact,
    base: &CheckedDictionary,
    limits: Limits,
) -> Result<FrozenBm25, String> {
    wire::validate(a, limits)?;
    let dictionary = base.dictionary();
    if a.meta.corpus_id != base.corpus_id()
        || a.meta.visibility.is_some()
        || a.analyzer_id != dictionary.analyzer_id()
        || a.analyzer_id != ANALYZER_VERSION
        || a.formula_id != FORMULA_V1
        || a.dictionary_registry_revision != base.registry_revision()
        || a.dictionary_mapping_fingerprint.sha256 != digest_hex(dictionary.fingerprint())
        || a.population_snapshot.corpus_id != base.corpus_id()
        || a.zero_token_documents >= a.document_count
        || !a.k1.is_finite()
        || a.k1 <= 0.0
        || !a.b.is_finite()
        || !(0.0..=1.0).contains(&a.b)
    {
        return Err("statistics corpus, dictionary, population or formula mismatch".into());
    }
    let nonempty = a.document_count - a.zero_token_documents;
    if nonempty > a.total_tokens || a.document_frequencies.len() > dictionary.len() {
        return Err("statistics population counts are impossible".into());
    }
    let ids: BTreeSet<_> = dictionary.term_ids().collect();
    let mut previous = 0;
    let mut sum = 0;
    let mut df = BTreeMap::new();
    for item in &a.document_frequencies {
        if item.term_id <= previous
            || !ids.contains(&item.term_id)
            || item.document_count > nonempty
            || item.document_count > a.total_tokens - sum
        {
            return Err("statistics DF is unsorted, foreign or exceeds population".into());
        }
        previous = item.term_id;
        sum += item.document_count;
        df.insert(item.term_id, item.document_count);
    }
    if sum < nonempty {
        return Err("DF coverage omits nonempty documents".into());
    }
    FrozenBm25::from_checked(
        a.analyzer_id.clone(),
        dictionary.revision().to_owned(),
        dictionary.fingerprint(),
        a.document_count,
        a.total_tokens,
        df,
        a.k1,
        a.b,
    )
    .map_err(|e| format!("frozen statistics: {e:?}"))
}

pub(crate) fn digest_hex(digest: [u8; 32]) -> String {
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{indexing::lexical::Bm25Statistics, wire::common};
    use protobuf::{Message, MessageField};

    fn base() -> CheckedDictionary {
        CheckedDictionary::decode(
            include_bytes!("../../../../tests/fixtures/lexical-dictionary-v1.pb"),
            "corpus:one",
            None,
            Limits::default(),
        )
        .unwrap()
    }
    fn fixture() -> LexicalStatisticsArtifact {
        LexicalStatisticsArtifact::parse_from_bytes(include_bytes!(
            "../../../../tests/fixtures/lexical-statistics-v1.pb"
        ))
        .unwrap()
    }

    #[test]
    fn exported_statistics_match_shared_bytes_and_scores() {
        let base = base();
        let expected = fixture();
        let mut stats = Bm25Statistics::new(ANALYZER_VERSION).unwrap();
        // Reverse insertion order and reordered tokens preserve BM25 population.
        stats.upsert(ANALYZER_VERSION, "doc:two", ["izin"]).unwrap();
        stats
            .upsert(ANALYZER_VERSION, "doc:one", ["pasal", "izin", "pasal"])
            .unwrap();
        stats.upsert(ANALYZER_VERSION, "doc:empty", []).unwrap();
        let output = stats
            .freeze_artifact(
                expected.meta.as_ref().unwrap(),
                expected.population_snapshot.as_ref().unwrap(),
                &base,
                1.2,
                0.75,
                Limits::default(),
            )
            .unwrap();
        assert_eq!(
            output.write_to_bytes().unwrap(),
            include_bytes!("../../../../tests/fixtures/lexical-statistics-v1.pb")
        );
        let frozen =
            decode_statistics(&output.write_to_bytes().unwrap(), &base, Limits::default()).unwrap();
        let doc = frozen
            .encode_document(base.dictionary(), ["pasal", "izin", "pasal"])
            .unwrap();
        let (query, oov) = frozen
            .encode_query(base.dictionary(), ["izin", "izin", "pasal", "unknown"])
            .unwrap();
        assert_eq!(oov, 1);
        let dot: f64 = doc
            .values
            .iter()
            .zip(&query.values)
            .map(|(d, q)| f64::from(*d) * f64::from(*q))
            .sum();
        let reference = stats
            .score(
                ANALYZER_VERSION,
                "doc:one",
                ["izin", "izin", "pasal", "unknown"],
                1.2,
                0.75,
            )
            .unwrap();
        assert!((dot - reference).abs() < 1e-6);
        assert!((f64::from(query.values[0]) - 2.0 * (1.0 + 1.5_f64 / 2.5).ln()).abs() < 1e-6);
        stats
            .upsert(ANALYZER_VERSION, "doc:one", ["pasal"])
            .unwrap();
        let changed = stats
            .freeze_artifact(
                expected.meta.as_ref().unwrap(),
                expected.population_snapshot.as_ref().unwrap(),
                &base,
                1.2,
                0.75,
                Limits::default(),
            )
            .unwrap();
        assert_ne!(
            changed.population_fingerprint,
            expected.population_fingerprint
        );
    }

    #[test]
    fn rejects_population_formula_dictionary_and_budget_drift() {
        let base = base();
        for mode in 0..11 {
            let mut a = fixture();
            match mode {
                0 => a.meta.as_mut().unwrap().corpus_id = "other".into(),
                1 => a.population_snapshot.as_mut().unwrap().corpus_id = "other".into(),
                2 => a.dictionary_registry_revision += 1,
                3 => a.formula_id = "other".into(),
                4 => a.zero_token_documents = a.document_count,
                5 => a.document_frequencies[0].document_count = 3,
                6 => a.total_tokens = 2,
                7 => a.document_frequencies[1].term_id = 1,
                8 => a.document_frequencies[1].term_id = 3,
                9 => a.k1 = f64::NAN,
                _ => a.document_count = 5,
            }
            assert!(
                check_statistics(&a, &base, Limits::default()).is_err(),
                "mode {mode}"
            );
        }
        let raw = fixture().write_to_bytes().unwrap();
        assert!(decode_statistics(
            &raw,
            &base,
            Limits {
                max_bytes: raw.len() - 1,
                ..Limits::default()
            }
        )
        .is_err());
        let mut a = LexicalAnalyzerArtifact::parse_from_bytes(include_bytes!(
            "../../../../tests/fixtures/lexical-analyzer-artifact-v1.pb"
        ))
        .unwrap();
        check_analyzer(&a, "corpus:one", Limits::default()).unwrap();
        assert_eq!(
            build_analyzer(a.meta.as_ref().unwrap(), Limits::default()).unwrap(),
            a
        );
        a.maximum_document_terms += 1;
        assert!(check_analyzer(&a, "corpus:one", Limits::default()).is_err());
        // Unknown/reassigned mappings cannot be legitimized by keeping a hash label.
        let mut s = fixture();
        s.dictionary_mapping_fingerprint = MessageField::some(common::ContentHash {
            sha256: "a".repeat(64),
            ..Default::default()
        });
        assert!(check_statistics(&s, &base, Limits::default()).is_err());
    }
}
