// Reconstructs the exact positive registry identities consumed by complete BIND
// output, using its sourced observations, editions, regulations and provision paths.
// The broad canonical-registry observation is narrowed only by re-deriving the same
// exact-key algorithm; aliases, unknown lookup scopes and partial bindings cannot be
// silently dropped. Storage must verify each identity across the observed/target
// interval. No allocation or artifact mutation occurs. Work and unique identities
// are bounded; measure planning p95/RSS under benchmark-targets.yaml (unmeasured).
package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type DocumentRegistryIdentity struct {
	CanonicalID                string
	EntityType                 int16
	IdentityScope, IdentityKey string
}

type DocumentRegistryDependencies struct {
	ObservedRevision uint64
	Identities       []DocumentRegistryIdentity
}

func PlanDocumentRegistryDependencies(batch *pb.DocumentBatch, maximumIdentities int) (DocumentRegistryDependencies, error) {
	var result DocumentRegistryDependencies
	if maximumIdentities <= 0 || maximumIdentities > DefaultWireLimits.MaxItems {
		return result, errors.New("bounded document registry plan required")
	}
	if err := ValidateWire(batch, DefaultWireLimits); err != nil {
		return result, err
	}
	if !graphAssemblyKnownFields(batch.ProtoReflect()) || batch.Meta.SchemaVersion != 1 || batch.Meta.Visibility != nil || batch.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || len(batch.Regulations) == 0 || len(batch.Sources) == 0 || len(batch.Provisions) == 0 {
		return result, errors.New("complete registry-bound document required")
	}
	if len(batch.Sources)+len(batch.Observations)+len(batch.Editions)+len(batch.Regulations)+len(batch.Provisions) > maximumIdentities {
		return result, errors.New("document registry source work exceeds budget")
	}
	// Reuse the document boundary for duplicate records/dependencies, corpus and
	// reference closure. Its edge budget also bounds edition observation traversal.
	if err := ValidateDocumentBatchClosure(batch, maximumIdentities); err != nil {
		return result, err
	}
	for _, lookup := range batch.DependencyManifest.LookupScopeRevisions {
		if lookup.ScopeId != "canonical-registry" || lookup.EmptyResult || lookup.Revision == 0 || lookup.Revision > math.MaxInt64 || result.ObservedRevision != 0 {
			return result, errors.New("unsupported or ambiguous document registry observation")
		}
		result.ObservedRevision = lookup.Revision
	}
	if result.ObservedRevision == 0 {
		return result, errors.New("document lacks BIND registry observation")
	}
	regs := map[string]*pb.Regulation{}
	for _, reg := range batch.Regulations {
		if regs[reg.Meta.RecordId] != nil || reg.Meta.CorpusId != batch.Meta.CorpusId {
			return result, errors.New("duplicate or foreign regulation")
		}
		regs[reg.Meta.RecordId] = reg
	}
	sources := map[string]bool{}
	for _, source := range batch.Sources {
		if sources[source.Meta.RecordId] {
			return result, errors.New("duplicate source blob")
		}
		sources[source.Meta.RecordId] = true
	}
	observations := map[string]*pb.SourceObservation{}
	bySource := map[string][]*pb.SourceObservation{}
	for _, observation := range batch.Observations {
		if observations[observation.Meta.RecordId] != nil {
			return result, errors.New("duplicate identity observation")
		}
		observations[observation.Meta.RecordId] = observation
		if id := observation.GetSourceBlobId(); id != "" {
			if !sources[id] {
				return result, errors.New("identity observation refers to foreign source")
			}
			bySource[id] = append(bySource[id], observation)
		}
	}
	regBySource := map[string]string{}
	for _, edition := range batch.Editions {
		regID := edition.GetRegulationId()
		if regs[regID] == nil || len(edition.SourceRefs) == 0 {
			return result, errors.New("edition lacks sourced regulation binding")
		}
		for _, id := range edition.SourceRefs {
			observation := observations[id]
			if observation == nil || observation.GetSourceBlobId() == "" {
				return result, errors.New("edition identity observation is absent")
			}
			source := observation.GetSourceBlobId()
			if old := regBySource[source]; old != "" && old != regID {
				return result, errors.New("source has conflicting regulation bindings")
			}
			regBySource[source] = regID
		}
	}
	if len(regBySource) != len(sources) {
		return result, errors.New("registry bindings do not cover every source")
	}
	expected := map[string]DocumentRegistryIdentity{}
	byKey := map[string]string{}
	add := func(identity DocumentRegistryIdentity) error {
		if !validDocumentID(identity.CanonicalID) {
			return errors.New("invalid expected canonical ID")
		}
		if old, ok := expected[identity.CanonicalID]; ok && old != identity {
			return errors.New("canonical ID has conflicting exact keys")
		}
		key := identity.IdentityScope + "\x00" + identity.IdentityKey
		if old := byKey[key]; old != "" && old != identity.CanonicalID {
			return errors.New("exact key maps to multiple canonical IDs")
		}
		expected[identity.CanonicalID] = identity
		byKey[key] = identity.CanonicalID
		if len(expected) > maximumIdentities {
			return errors.New("document identity budget exceeded")
		}
		return nil
	}
	seenRegs := map[string]bool{}
	issuers := map[string]bool{}
	for source, regID := range regBySource {
		reg := regs[regID]
		candidate, problems := regulationCandidate(source, bySource[source], RegulationIdentityPolicy{Jurisdiction: reg.Jurisdiction, MaximumItems: maximumIdentities})
		if len(problems) != 0 || normalizeIdentityText(candidate.Kind) != normalizeIdentityText(reg.Kind) || normalizeIdentityText(candidate.OfficialNumber) != normalizeIdentityText(reg.OfficialNumber) || candidate.Year != reg.Year {
			return result, errors.New("regulation identity differs from source observations")
		}
		issuer, err := candidate.CanonicalIssuerClaim()
		if err != nil {
			return result, err
		}
		regulation, err := candidate.canonicalRegulationClaim(reg.IssuerId)
		if err != nil {
			return result, err
		}
		for _, identity := range []DocumentRegistryIdentity{{reg.IssuerId, issuer.EntityType, issuer.IdentityScope, issuer.IdentityKey}, {regID, regulation.EntityType, regulation.IdentityScope, regulation.IdentityKey}} {
			if err = add(identity); err != nil {
				return result, err
			}
		}
		issuers[reg.IssuerId] = true
		seenRegs[regID] = true
	}
	if len(seenRegs) != len(regs) {
		return result, errors.New("unsourced regulation in registry document")
	}
	for _, provision := range batch.Provisions {
		if regs[provision.RegulationId] == nil || len(provision.StructuralPath) == 0 || provision.Meta.CorpusId != batch.Meta.CorpusId {
			return result, errors.New("provision has no exact regulation/path binding")
		}
		if err := add(DocumentRegistryIdentity{provision.Meta.RecordId, CanonicalEntityTypeProvision, ProvisionIdentityKeyNamespace, provisionIdentityKey(provision.RegulationId, provision.StructuralPath)}); err != nil {
			return result, err
		}
	}
	// BIND records the issuer identity at the same broad revision. Preserve this
	// provenance check instead of accepting an arbitrary list of IDs in a document.
	for _, dependency := range batch.DependencyManifest.Dependencies {
		if !issuers[dependency.DependencyId] {
			continue
		}
		digest := sha256.Sum256([]byte(strings.Join([]string{"canonical-registry", "organization", dependency.DependencyId, strconv.FormatUint(result.ObservedRevision, 10)}, "\x00")))
		if dependency.Fingerprint.Sha256 != fmt.Sprintf("%x", digest) {
			return result, errors.New("issuer dependency differs from BIND revision")
		}
		delete(issuers, dependency.DependencyId)
	}
	if len(issuers) != 0 {
		return result, errors.New("BIND issuer dependency missing")
	}
	result.Identities = make([]DocumentRegistryIdentity, 0, len(expected))
	for _, identity := range expected {
		result.Identities = append(result.Identities, identity)
	}
	sort.Slice(result.Identities, func(i, j int) bool { return result.Identities[i].CanonicalID < result.Identities[j].CanonicalID })
	return result, nil
}
