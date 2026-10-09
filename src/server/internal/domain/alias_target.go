// Carries a provisional target's original reviewed occurrence into a new alias
// review. This evidence is independent of the new mention. Rebuilding the original
// source anchor prevents a supplied label or canonical ID from serving as proof.
// Storage authenticates the origin receipt and historical rows before mutation.
// Shared review byte/context limits apply; measure false links and review p95
// against benchmark-targets.yaml, whose acceptance remains unmeasured.
package domain

import (
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type ProvisionalAliasOriginRef struct {
	Operation, SourceArtifactID, MentionID string
	PolicyHash, PlanHash                   string
	CreatedRevision                        uint64
}

type ProvisionalAliasOrigin struct {
	Ref                    ProvisionalAliasOriginRef
	Source, Document, Text AliasSourceArtifact
}

func buildAliasTargetPreview(in SourcedAliasInput) (*SourcedAliasPreview, error) {
	o := in.TargetOrigin
	if o == nil || in.CreateProvisional || in.ExistingProfile == nil || len(in.ExistingProfile.IdentityKeys) != 1 || !validDocumentID(o.Ref.Operation) ||
		o.Ref.CreatedRevision < 2 || o.Ref.CreatedRevision > in.ExpectedRevision ||
		o.Ref.SourceArtifactID != o.Source.Ref.GetArtifactId() || !proto.Equal(o.Document.Ref, in.TargetDocument.Ref) {
		return nil, errors.New("existing provisional target requires its reviewed source origin")
	}
	originalInput := SourcedAliasInput{CreateProvisional: true, Corpus: in.Corpus, AuthScope: in.AuthScope,
		MentionID: o.Ref.MentionID, Scope: in.Scope, PreferredLabel: in.PreferredLabel, ExpectedRevision: o.Ref.CreatedRevision - 1,
		Source: o.Source, Document: o.Document, TargetDocument: o.Document, Text: o.Text, Policy: in.Policy}
	p, err := BuildSourcedAliasPreview(originalInput)
	if err != nil {
		return nil, err
	}
	e := p.Registration.Entity
	if e.Meta.RecordId != in.CanonicalID || e.EntityType != in.ExistingProfile.EntityType ||
		!proto.Equal(e.IdentityKeys[0], in.ExistingProfile.GetIdentityKeys()[0]) {
		return nil, errors.New("target source occurrence does not identify selected profile")
	}
	if err := ValidateWire(&pb.ContentHash{Sha256: o.Ref.PolicyHash}, DefaultWireLimits); err != nil {
		return nil, err
	}
	originalHash, err := sourcedAliasPlanHash(originalInput, p.Registration.Entity, p.Registration.Alias, o.Ref.PolicyHash, nil, "")
	if err != nil {
		return nil, err
	}
	if originalHash != o.Ref.PlanHash {
		return nil, errors.New("original provisional approval plan differs from source/profile/policy")
	}
	p.PlanHash = originalHash
	return p, nil
}
