// Defines exact semantic candidate lookup keys shared by workflow and PostgreSQL.
// Legal scope and normalized text are chosen by the caller's pinned policy; these
// records carry no implicit fuzzy identity decision. Measure blocking coverage,
// candidate count, and p95/p99 against configs/benchmark-targets.yaml.
package domain

// RegistryLookupScope is an exact key in the versioned alias registry.
type RegistryLookupScope struct {
	EntityType       string
	CanonicalScope   string
	NormalizedLookup string
}

// RegistryCandidatePlan enumerates every scope to read for one extracted mention.
type RegistryCandidatePlan struct {
	MentionID string
	Scopes    []RegistryLookupScope
}
