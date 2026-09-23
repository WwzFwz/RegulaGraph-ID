//! Membentuk kelompok kandidat penyamaan identitas dengan sinyal murah sebelum keputusan resolver.
//!
//! Peran dalam komponen:
//! Mengendalikan biaya resolution pada kumpulan mention besar.
//!
//! Kontrak integrasi dan perhatian implementasi:
//! Blocking bukan keputusan merge; alias lintas singkatan yang tidak berbagi token memerlukan jalur kandidat lain.
//!
//! Benchmark dan gate penerimaan:
//! [RESOLUTION] Ukur pairwise precision/recall/F1, false merge, false split, mention yang hilang, waktu per batch, dan biaya. Gate: semua mention tetap terlacak; pasal dari peraturan berbeda tidak digabung hanya karena nama sama. Blocking harus diukur juga terhadap pasangan benar yang terlewat.
//! Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
//! Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//!
//! Status: indeks kandidat scoped berbasis label/alias registry aktif sebagai library; belum
//! terhubung ke RESOLVE worker. Collision tetap ambigu; overflow eksplisit dan tidak dipangkas.
//! Ukur candidate recall/reduction pada gold set sebelum menganggap blocking efektif.

use std::collections::{BTreeSet, HashMap};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ScopedCanonical<'a> {
    pub canonical_id: &'a str,
    pub entity_type: &'a str,
    pub scope: &'a str,
    /// Preferred label and sourced aliases supplied by the registry; no synonym inference.
    pub surfaces: &'a [&'a str],
}

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct LookupKey {
    pub entity_type: String,
    pub scope: String,
    pub normalized_surface: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum BlockingError {
    EmptyField,
    EmptySurfaces,
    ConflictingCanonicalScope,
    CandidateOverflow { count: usize, limit: usize },
    InvalidLimit,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct BlockingResult {
    /// Keep this key even for empty lookup so incremental updates can invalidate a negative hit.
    pub lookup_key: LookupKey,
    /// Sorted registry IDs are candidates only; this does not authorize a merge.
    pub candidate_ids: Vec<String>,
}

#[derive(Clone, Debug, Default)]
pub struct BlockingIndex {
    postings: HashMap<LookupKey, BTreeSet<String>>,
}

impl BlockingIndex {
    pub fn build(entities: &[ScopedCanonical<'_>]) -> Result<Self, BlockingError> {
        let mut index = Self::default();
        let mut owners = HashMap::<&str, (&str, &str)>::new();
        for entity in entities {
            if entity.canonical_id.trim().is_empty()
                || entity.entity_type.trim().is_empty()
                || entity.scope.trim().is_empty()
            {
                return Err(BlockingError::EmptyField);
            }
            if entity.surfaces.is_empty() {
                return Err(BlockingError::EmptySurfaces);
            }
            if let Some(previous) =
                owners.insert(entity.canonical_id, (entity.entity_type, entity.scope))
            {
                if previous != (entity.entity_type, entity.scope) {
                    return Err(BlockingError::ConflictingCanonicalScope);
                }
            }
            for surface in entity.surfaces {
                let key = LookupKey::new(entity.entity_type, entity.scope, surface)?;
                index
                    .postings
                    .entry(key)
                    .or_default()
                    .insert(entity.canonical_id.to_owned());
            }
        }
        Ok(index)
    }

    pub fn lookup(
        &self,
        entity_type: &str,
        scope: &str,
        surface: &str,
        limit: usize,
    ) -> Result<BlockingResult, BlockingError> {
        if limit == 0 {
            return Err(BlockingError::InvalidLimit);
        }
        let lookup_key = LookupKey::new(entity_type, scope, surface)?;
        let candidates = self.postings.get(&lookup_key);
        let count = candidates.map_or(0, BTreeSet::len);
        if count > limit {
            return Err(BlockingError::CandidateOverflow { count, limit });
        }
        Ok(BlockingResult {
            lookup_key,
            candidate_ids: candidates
                .map(|ids| ids.iter().cloned().collect())
                .unwrap_or_default(),
        })
    }
}

impl LookupKey {
    fn new(entity_type: &str, scope: &str, surface: &str) -> Result<Self, BlockingError> {
        if entity_type.trim().is_empty() || scope.trim().is_empty() {
            return Err(BlockingError::EmptyField);
        }
        // Preserve punctuation and digits: legal numbers and negation are identity signals.
        let normalized_surface = surface
            .split_whitespace()
            .collect::<Vec<_>>()
            .join(" ")
            .to_lowercase();
        if normalized_surface.is_empty() {
            return Err(BlockingError::EmptyField);
        }
        Ok(Self {
            entity_type: entity_type.to_owned(),
            scope: scope.to_owned(),
            normalized_surface,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn article_number_is_scoped_to_its_regulation() {
        let index = BlockingIndex::build(&[
            ScopedCanonical {
                canonical_id: "a:1",
                entity_type: "provision",
                scope: "law:a",
                surfaces: &["Pasal 1"],
            },
            ScopedCanonical {
                canonical_id: "b:1",
                entity_type: "provision",
                scope: "law:b",
                surfaces: &["Pasal 1"],
            },
        ])
        .unwrap();
        assert_eq!(
            index
                .lookup("provision", "law:a", " PASAL  1 ", 2)
                .unwrap()
                .candidate_ids,
            ["a:1"]
        );
        assert!(index
            .lookup("organization", "law:a", "Pasal 1", 2)
            .unwrap()
            .candidate_ids
            .is_empty());
    }

    #[test]
    fn ambiguous_acronym_is_retained_and_overflow_is_reported() {
        let index = BlockingIndex::build(&[
            ScopedCanonical {
                canonical_id: "org:a",
                entity_type: "organization",
                scope: "national",
                surfaces: &["Badan A", "BA"],
            },
            ScopedCanonical {
                canonical_id: "org:b",
                entity_type: "organization",
                scope: "national",
                surfaces: &["Badan B", "BA"],
            },
        ])
        .unwrap();
        assert_eq!(
            index
                .lookup("organization", "national", "ba", 2)
                .unwrap()
                .candidate_ids,
            ["org:a", "org:b"]
        );
        assert_eq!(
            index.lookup("organization", "national", "BA", 1),
            Err(BlockingError::CandidateOverflow { count: 2, limit: 1 })
        );
    }

    #[test]
    fn negative_lookup_keeps_dependency_key() {
        let found = BlockingIndex::default()
            .lookup("legal_concept", "national", "izin baru", 3)
            .unwrap();
        assert!(found.candidate_ids.is_empty());
        assert_eq!(found.lookup_key.normalized_surface, "izin baru");
    }

    #[test]
    fn conflicting_canonical_scope_is_rejected() {
        assert_eq!(
            BlockingIndex::build(&[
                ScopedCanonical {
                    canonical_id: "same",
                    entity_type: "organization",
                    scope: "national",
                    surfaces: &["A"]
                },
                ScopedCanonical {
                    canonical_id: "same",
                    entity_type: "organization",
                    scope: "regional",
                    surfaces: &["A"]
                },
            ])
            .unwrap_err(),
            BlockingError::ConflictingCanonicalScope
        );
    }
}
