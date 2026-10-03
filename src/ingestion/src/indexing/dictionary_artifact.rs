//! Consumes a bounded typed dictionary snapshot at the Go-to-Rust INDEX boundary.
//! The checked object owns its mapping and inherits ancestry only from a supplied,
//! checked parent with the same corpus/analyzer. Artifact byte hashes and registry
//! authority remain caller responsibilities before publication. Load once per
//! generation; measure decoding/lookup RSS and p95 under benchmark-targets.yaml.
//! Required production quality/performance gates remain unmeasured.

use super::dictionary::LexicalDictionary;
use crate::domain::wire::{self, Limits};
use crate::wire::evidence::LexicalDictionaryArtifact;
use protobuf::MessageFull;

#[derive(Debug)]
pub struct CheckedDictionary {
    corpus_id: String,
    registry_revision: u64,
    dictionary: LexicalDictionary,
}

impl CheckedDictionary {
    pub fn corpus_id(&self) -> &str {
        &self.corpus_id
    }
    pub fn registry_revision(&self) -> u64 {
        self.registry_revision
    }
    pub fn dictionary(&self) -> &LexicalDictionary {
        &self.dictionary
    }

    /// Bytes must come from the hash-verified artifact store; this gate bounds
    /// decoding and checks content, but does not authenticate a registry writer.
    pub fn decode(
        raw: &[u8],
        corpus: &str,
        parent: Option<&Self>,
        limits: Limits,
    ) -> Result<Self, String> {
        let decoded = wire::decode(raw, &LexicalDictionaryArtifact::descriptor(), limits)?;
        let artifact = decoded
            .downcast_ref::<LexicalDictionaryArtifact>()
            .ok_or("dictionary descriptor mismatch")?;
        Self::check_content(artifact, corpus, parent)
    }

    pub fn from_artifact(
        artifact: &LexicalDictionaryArtifact,
        corpus: &str,
        parent: Option<&Self>,
        limits: Limits,
    ) -> Result<Self, String> {
        wire::validate(artifact, limits)?;
        Self::check_content(artifact, corpus, parent)
    }

    fn check_content(
        a: &LexicalDictionaryArtifact,
        corpus: &str,
        parent: Option<&Self>,
    ) -> Result<Self, String> {
        let meta = a.meta.as_ref().ok_or("dictionary metadata absent")?;
        if meta.corpus_id != corpus
            || meta.visibility.is_some()
            || a.registry_revision == 0
            || a.registry_revision > i64::MAX as u64
            || a.entries.len() > 10_000_000
        {
            return Err("dictionary corpus, revision or size mismatch".into());
        }
        if a.parent_registry_revision.is_some() != a.parent_mapping_fingerprint.is_some()
            || a.parent_registry_revision.is_some() != parent.is_some()
        {
            return Err("dictionary requires exactly its declared checked parent".into());
        }
        if let Some(p) = parent {
            if p.corpus_id != corpus
                || p.dictionary.analyzer_id() != a.analyzer_id
                || Some(p.registry_revision) != a.parent_registry_revision
                || p.registry_revision >= a.registry_revision
                || digest_hex(p.dictionary.fingerprint()) != a.parent_mapping_fingerprint.sha256
            {
                return Err(
                    "dictionary parent corpus, analyzer, revision or digest mismatch".into(),
                );
            }
        }
        if a.entries
            .windows(2)
            .any(|pair| pair[0].term >= pair[1].term)
        {
            return Err("dictionary entries are not strictly sorted".into());
        }
        let entries = a.entries.iter().map(|e| (e.term.clone(), e.term_id));
        let revision = format!("lexrev:{}", a.registry_revision);
        let dictionary = match parent {
            Some(p) => {
                LexicalDictionary::from_allocated_descendant(&p.dictionary, revision, entries)
            }
            None => LexicalDictionary::from_allocated_entries(&a.analyzer_id, revision, entries),
        }
        .map_err(|error| format!("dictionary mapping: {error:?}"))?;
        if digest_hex(dictionary.fingerprint()) != a.mapping_fingerprint.sha256 {
            return Err("dictionary mapping fingerprint mismatch".into());
        }
        Ok(Self {
            corpus_id: corpus.to_owned(),
            registry_revision: a.registry_revision,
            dictionary,
        })
    }
}

fn digest_hex(digest: [u8; 32]) -> String {
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::wire::{common, evidence};
    use protobuf::{Message, MessageField};

    fn root() -> LexicalDictionaryArtifact {
        LexicalDictionaryArtifact {
            meta: MessageField::some(common::RecordMeta {
                schema_version: 1,
                corpus_id: "corpus:one".into(),
                record_id: "dictionary:2".into(),
                ..Default::default()
            }),
            analyzer_id: "regulagraph-lexical-nfc-ascii-v1".into(),
            registry_revision: 2,
            mapping_fingerprint: MessageField::some(common::ContentHash {
                sha256: "8199cf3ca12cb04c039ec1a296d025254b7a09ba1f675512d78c289fd80c4a7a".into(),
                ..Default::default()
            }),
            entries: vec![
                evidence::LexicalDictionaryEntry {
                    term: "izin".into(),
                    term_id: 1,
                    ..Default::default()
                },
                evidence::LexicalDictionaryEntry {
                    term: "pasal".into(),
                    term_id: 2,
                    ..Default::default()
                },
            ],
            ..Default::default()
        }
    }

    #[test]
    fn reads_go_snapshot_and_checked_descendant() {
        let a = root();
        let shared = include_bytes!("../../../../tests/fixtures/lexical-dictionary-v1.pb");
        assert_eq!(a.write_to_bytes().unwrap(), shared.as_slice());
        let parent =
            CheckedDictionary::decode(shared, "corpus:one", None, Limits::default()).unwrap();
        assert_eq!(parent.dictionary().term_id("pasal"), Some(2));
        let mut child = a.clone();
        child.registry_revision = 3;
        child.parent_registry_revision = Some(2);
        child.parent_mapping_fingerprint = a.mapping_fingerprint.clone();
        child.entries.push(evidence::LexicalDictionaryEntry {
            term: "tidak".into(),
            term_id: 3,
            ..Default::default()
        });
        let mapping = LexicalDictionary::from_allocated_descendant(
            parent.dictionary(),
            "lexrev:3",
            [("izin".into(), 1), ("pasal".into(), 2), ("tidak".into(), 3)],
        )
        .unwrap();
        child.mapping_fingerprint = MessageField::some(common::ContentHash {
            sha256: digest_hex(mapping.fingerprint()),
            ..Default::default()
        });
        let loaded = CheckedDictionary::from_artifact(
            &child,
            "corpus:one",
            Some(&parent),
            Limits::default(),
        )
        .unwrap();
        assert!(loaded
            .dictionary()
            .descends_from("lexrev:2", parent.dictionary().fingerprint()));
        assert!(
            CheckedDictionary::from_artifact(&child, "corpus:one", None, Limits::default())
                .is_err()
        );
        child.entries[0].term_id = 4;
        let reassigned = LexicalDictionary::from_allocated_entries(
            &child.analyzer_id,
            "lexrev:3",
            child
                .entries
                .iter()
                .map(|entry| (entry.term.clone(), entry.term_id)),
        )
        .unwrap();
        child.mapping_fingerprint.as_mut().unwrap().sha256 = digest_hex(reassigned.fingerprint());
        assert!(CheckedDictionary::from_artifact(
            &child,
            "corpus:one",
            Some(&parent),
            Limits::default()
        )
        .is_err());
    }

    #[test]
    fn rejects_drift_order_foreign_corpus_and_budget() {
        let a = root();
        for (max_items, valid) in [(6, true), (5, false)] {
            assert_eq!(
                CheckedDictionary::from_artifact(
                    &a,
                    "corpus:one",
                    None,
                    Limits {
                        max_items,
                        ..Limits::default()
                    }
                )
                .is_ok(),
                valid
            );
        }
        assert!(
            CheckedDictionary::from_artifact(&a, "corpus:other", None, Limits::default()).is_err()
        );
        let mut swapped = a.clone();
        swapped.entries.swap(0, 1);
        assert!(
            CheckedDictionary::from_artifact(&swapped, "corpus:one", None, Limits::default())
                .is_err()
        );
        let mut drift = a.clone();
        drift.mapping_fingerprint.as_mut().unwrap().sha256 = "a".repeat(64);
        assert!(
            CheckedDictionary::from_artifact(&drift, "corpus:one", None, Limits::default())
                .is_err()
        );
        let raw = a.write_to_bytes().unwrap();
        assert!(CheckedDictionary::decode(
            &raw,
            "corpus:one",
            None,
            Limits {
                max_bytes: 4,
                ..Limits::default()
            }
        )
        .is_err());
        let mut missing = a;
        missing.parent_registry_revision = Some(1);
        assert!(
            CheckedDictionary::from_artifact(&missing, "corpus:one", None, Limits::default())
                .is_err()
        );
    }
}
