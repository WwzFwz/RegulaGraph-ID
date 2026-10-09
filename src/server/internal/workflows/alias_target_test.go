// Checks the pure review boundary for source-origin targets. This does not grant
// storage authority; integration tests separately authenticate origin receipts.
// Tests exercise changed evidence and immutable target profiles, not legal truth.
package workflows

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestExistingProvisionalAliasPreview(t *testing.T) {
	base, source, _ := provisionalAliasFixture(t, &bindingStoreFake{})
	creation, err := domain.BuildSourcedAliasPreview(base)
	if err != nil {
		t.Fatal(err)
	}
	in := base
	in.CreateProvisional = false
	in.ExpectedRevision++
	in.CanonicalID = creation.Registration.Entity.Meta.RecordId
	in.ExistingProfile = creation.Registration.Entity
	pin, _ := in.Policy.Fingerprint()
	in.TargetOrigin = &domain.ProvisionalAliasOrigin{Ref: domain.ProvisionalAliasOriginRef{Operation: "origin:review", PolicyHash: pin.Sha256, PlanHash: creation.PlanHash, SourceArtifactID: base.Source.Ref.ArtifactId, MentionID: base.MentionID, CreatedRevision: in.ExpectedRevision}, Source: base.Source, Document: base.Document, Text: base.Text}
	source.Mentions[0].Meta.RecordId = "mention:new"
	in.MentionID = "mention:new"
	in.Source = sourcedAliasProto(t, domain.ExtractionBatchMediaType, source)
	p, err := domain.BuildSourcedAliasPreview(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.TargetOrigin == nil || p.TargetOrigin.Mention.Meta.RecordId == p.Mention.Meta.RecordId {
		t.Fatal("source and target collapsed")
	}
	for _, mode := range []string{"no-profile", "extra-key", "wrong-kind", "changed-label", "future-origin", "changed-origin-plan", "changed-origin-policy", "wrong-artifact", "new-creation", "different-origin"} {
		t.Run(mode, func(t *testing.T) {
			x := in
			o := *in.TargetOrigin
			x.TargetOrigin = &o
			x.ExistingProfile = proto.Clone(in.ExistingProfile).(*pb.CanonicalEntity)
			switch mode {
			case "no-profile":
				x.ExistingProfile = nil
			case "extra-key":
				x.ExistingProfile.IdentityKeys = append(x.ExistingProfile.IdentityKeys, &pb.IdentityKey{Namespace: "invented", Value: "invented"})
			case "wrong-kind":
				x.ExistingProfile.EntityType = "activity"
			case "changed-label":
				x.PreferredLabel = "invented target label"
			case "changed-origin-plan":
				o.Ref.PlanHash = strings.Repeat("0", 64)
			case "changed-origin-policy":
				o.Ref.PolicyHash = strings.Repeat("0", 64)
			case "future-origin":
				o.Ref.CreatedRevision++
			case "wrong-artifact":
				o.Ref.SourceArtifactID = "artifact:wrong"
			case "new-creation":
				x.CreateProvisional = true
			case "different-origin":
				o.Ref.MentionID = "mention:other"
			}
			if _, e := domain.BuildSourcedAliasPreview(x); e == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
	// The proof is owned by the preview; editing its visible evidence must not
	// mutate the source inputs or change a later independently rebuilt preview.
	p.TargetOrigin.Mention.SurfaceForm = "mutated display"
	unchanged, e := domain.BuildSourcedAliasPreview(in)
	if e != nil || unchanged.PlanHash != p.PlanHash || unchanged.TargetOrigin.Mention.SurfaceForm == "mutated display" {
		t.Fatal("display mutated review input", e)
	}
}
