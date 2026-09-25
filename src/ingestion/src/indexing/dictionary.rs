//! Memvalidasi dictionary istilah yang dialokasikan Go untuk satu analyzer/revision.
//!
//! Rust hanya membaca pasangan term–ID immutable; allocator transaksi dimiliki
//! PostgreSQL/Go. Descendant dibentuk dari parent tervalidasi dan harus mempertahankan
//! semua pasangan ID lama. Digest revision mencegah nama revision yang sama dengan
//! mapping berbeda dipakai untuk bobot sparse generation lama.
//!
//! Benchmark: ukur ukuran dictionary, waktu load/lookup, RSS serta OOV pada
//! configs/benchmark-targets.yaml; status target REQUIRED_UNMEASURED.

use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};

const MAX_TERMS: usize = 10_000_000;
const MAX_TERM_BYTES: usize = 256;
const MAX_ID_BYTES: usize = 256;
const MAX_LINEAGE_REVISIONS: usize = 16_384;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DictionaryError {
    EmptyIdentity,
    InvalidIdentity,
    InvalidTerm,
    InvalidID,
    DuplicateTerm,
    DuplicateID,
    TooManyTerms,
    InvalidLineage,
    ReassignedTerm,
}

/// The local lineage map proves an ancestor chain only when every parent artifact
/// was verified. Physical index reuse also needs PostgreSQL's authoritative proof.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LexicalDictionary {
    analyzer_id: String,
    revision: String,
    terms: BTreeMap<String, u32>,
    fingerprint: [u8; 32],
    lineage: BTreeMap<String, [u8; 32]>,
}

impl LexicalDictionary {
    pub fn from_allocated_entries(
        analyzer_id: impl Into<String>,
        revision: impl Into<String>,
        entries: impl IntoIterator<Item = (String, u32)>,
    ) -> Result<Self, DictionaryError> {
        let analyzer_id = analyzer_id.into();
        let revision = revision.into();
        if analyzer_id.is_empty() || revision.is_empty() {
            return Err(DictionaryError::EmptyIdentity);
        }
        if !valid_id(&analyzer_id) || !valid_id(&revision) {
            return Err(DictionaryError::InvalidIdentity);
        }
        let mut terms = BTreeMap::new();
        let mut seen_ids = BTreeSet::new();
        for (term, id) in entries {
            if terms.len() == MAX_TERMS {
                return Err(DictionaryError::TooManyTerms);
            }
            if term.is_empty() || term.len() > MAX_TERM_BYTES || term.chars().any(char::is_control)
            {
                return Err(DictionaryError::InvalidTerm);
            }
            if id == 0 {
                return Err(DictionaryError::InvalidID);
            }
            if terms.contains_key(&term) {
                return Err(DictionaryError::DuplicateTerm);
            }
            if !seen_ids.insert(id) {
                return Err(DictionaryError::DuplicateID);
            }
            terms.insert(term, id);
        }
        let fingerprint = fingerprint(&analyzer_id, &revision, &terms);
        Ok(Self {
            analyzer_id,
            revision: revision.clone(),
            terms,
            fingerprint,
            lineage: BTreeMap::from([(revision, fingerprint)]),
        })
    }

    /// Builds a verified descendant from the complete allocated dictionary. The
    /// caller must load the parent from a verified immutable artifact; this check
    /// is a local safety boundary, not a substitute for PostgreSQL lineage proof.
    pub fn from_allocated_descendant(
        parent: &Self,
        revision: impl Into<String>,
        entries: impl IntoIterator<Item = (String, u32)>,
    ) -> Result<Self, DictionaryError> {
        let revision = revision.into();
        if revision.is_empty()
            || parent.lineage.contains_key(&revision)
            || parent.lineage.len() == MAX_LINEAGE_REVISIONS
        {
            return Err(DictionaryError::InvalidLineage);
        }
        let mut child = Self::from_allocated_entries(&parent.analyzer_id, revision, entries)?;
        if parent
            .terms
            .iter()
            .any(|(term, id)| child.terms.get(term) != Some(id))
        {
            return Err(DictionaryError::ReassignedTerm);
        }
        child.lineage = parent.lineage.clone();
        child
            .lineage
            .insert(child.revision.clone(), child.fingerprint);
        Ok(child)
    }

    pub fn analyzer_id(&self) -> &str {
        &self.analyzer_id
    }
    pub fn revision(&self) -> &str {
        &self.revision
    }
    pub fn fingerprint(&self) -> [u8; 32] {
        self.fingerprint
    }
    pub fn descends_from(&self, revision: &str, fingerprint: [u8; 32]) -> bool {
        self.lineage.get(revision) == Some(&fingerprint)
    }
    pub fn term_id(&self, term: &str) -> Option<u32> {
        self.terms.get(term).copied()
    }
    pub fn len(&self) -> usize {
        self.terms.len()
    }
    pub fn is_empty(&self) -> bool {
        self.terms.is_empty()
    }
}

fn fingerprint(analyzer: &str, revision: &str, terms: &BTreeMap<String, u32>) -> [u8; 32] {
    let mut digest = Sha256::new();
    digest.update(b"regulagraph-lexical-dictionary-v1\0");
    for field in [analyzer, revision] {
        digest.update((field.len() as u64).to_be_bytes());
        digest.update(field.as_bytes());
    }
    digest.update((terms.len() as u64).to_be_bytes());
    for (term, id) in terms {
        digest.update((term.len() as u64).to_be_bytes());
        digest.update(term.as_bytes());
        digest.update(id.to_be_bytes());
    }
    digest.finalize().into()
}

fn valid_id(id: &str) -> bool {
    id.len() <= MAX_ID_BYTES && id.bytes().all(|byte| (33..=126).contains(&byte))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_collisions_without_reassigning_ids() {
        let good = LexicalDictionary::from_allocated_entries(
            "a",
            "r1",
            [("pasal".into(), 5), ("12/2020".into(), 2)],
        )
        .unwrap();
        assert_eq!(good.term_id("pasal"), Some(5));
        assert_eq!(good.term_id("unknown"), None);
        assert_eq!(
            LexicalDictionary::from_allocated_entries(
                "a",
                "r1",
                [("a".into(), 2), ("b".into(), 2)]
            ),
            Err(DictionaryError::DuplicateID)
        );
        assert_eq!(
            LexicalDictionary::from_allocated_entries(
                "a",
                "r1",
                [("a".into(), 2), ("a".into(), 3)]
            ),
            Err(DictionaryError::DuplicateTerm)
        );
        assert_eq!(
            LexicalDictionary::from_allocated_entries("a", "r1", [("a".into(), 0)]),
            Err(DictionaryError::InvalidID)
        );
    }

    #[test]
    fn descendant_preserves_ids_and_rejects_sibling_or_reassignment() {
        let base =
            LexicalDictionary::from_allocated_entries("analyzer-v1", "r1", [("pasal".into(), 7)])
                .unwrap();
        let child = LexicalDictionary::from_allocated_descendant(
            &base,
            "r2",
            [("pasal".into(), 7), ("izin".into(), 11)],
        )
        .unwrap();
        assert!(child.descends_from(base.revision(), base.fingerprint()));
        assert!(!base.descends_from(child.revision(), child.fingerprint()));
        let sibling = LexicalDictionary::from_allocated_descendant(
            &base,
            "r2-sibling",
            [("pasal".into(), 7), ("baru".into(), 12)],
        )
        .unwrap();
        assert!(!child.descends_from(sibling.revision(), sibling.fingerprint()));
        assert_eq!(
            LexicalDictionary::from_allocated_descendant(&base, "r3", [("pasal".into(), 8)],),
            Err(DictionaryError::ReassignedTerm)
        );
        assert_eq!(
            LexicalDictionary::from_allocated_descendant(&base, "r1", [("pasal".into(), 7)],),
            Err(DictionaryError::InvalidLineage)
        );
        let impostor =
            LexicalDictionary::from_allocated_entries("analyzer-v1", "r1", [("pasal".into(), 8)])
                .unwrap();
        assert!(!child.descends_from(impostor.revision(), impostor.fingerprint()));
        assert_eq!(
            LexicalDictionary::from_allocated_entries(
                "a".repeat(MAX_ID_BYTES + 1),
                "r",
                [("pasal".into(), 7)],
            ),
            Err(DictionaryError::InvalidIdentity)
        );
        assert_eq!(
            LexicalDictionary::from_allocated_entries("a", "r\n2", [("pasal".into(), 7)]),
            Err(DictionaryError::InvalidIdentity)
        );
        let mut capped = base.clone();
        for i in 1..MAX_LINEAGE_REVISIONS {
            capped.lineage.insert(format!("revision-{i}"), [0; 32]);
        }
        assert_eq!(capped.lineage.len(), MAX_LINEAGE_REVISIONS);
        assert!(matches!(
            LexicalDictionary::from_allocated_descendant(&capped, "next", [("pasal".into(), 7)],),
            Err(DictionaryError::InvalidLineage)
        ));
    }
}
