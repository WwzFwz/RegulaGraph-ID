// Exercises occurrence-key stability independently of extraction/chunk identities
// and source-sensitive changes. These fixtures verify deterministic contracts,
// not that two textual occurrences represent one legal entity.
package workflows

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func TestProvisionalAliasSourceIdentity(t *testing.T) {
	in, source, _ := provisionalAliasFixture(t, &bindingStoreFake{})
	// Place the occurrence in both the root and child provision, so the model
	// can legitimately select different source-ref subsets for the same span.
	doc := new(pb.DocumentBatch)
	if err := proto.Unmarshal(in.Document.Bytes, doc); err != nil {
		t.Fatal(err)
	}
	in.Text = sourcedAliasArtifact("text/plain;charset=utf-8", []byte(strings.Repeat(" ", 10)+source.Mentions[0].SurfaceForm+strings.Repeat(" ", 90-len(source.Mentions[0].SurfaceForm))))
	doc.TextArtifacts[0].NormalizedTextRef = in.Text.Ref
	for _, v := range doc.Versions {
		v.TextRef = proto.Clone(in.Text.Ref).(*pb.ArtifactRef)
	}
	in.Document = sourcedAliasProto(t, domain.DocumentBatchMediaType, doc)
	in.TargetDocument = in.Document
	source.Mentions[0].TextSpan.StartByte = 10
	source.Mentions[0].TextSpan.EndByte += 10
	source.SourceDocumentBatch = in.Document.Ref
	source.Dependencies.Dependencies[0].DependencyId = in.Document.Ref.ArtifactId
	source.Dependencies.Dependencies[0].Fingerprint = in.Document.Ref.ContentHash
	in.Source = sourcedAliasProto(t, domain.ExtractionBatchMediaType, source)
	base, err := domain.BuildSourcedAliasPreview(in)
	if err != nil {
		t.Fatal(err)
	}
	baseID := base.Registration.Entity.Meta.RecordId
	if baseID != "canonical:provisional:92045ff768f117337ccf917de21ce665aadbfcc934885cd578dec8090432cc75" {
		t.Fatal("v1 identity namespace changed", baseID)
	}
	for _, mode := range []string{"mention", "model", "chunk", "parser", "normalizer", "text", "span", "scope", "type", "source-ref-subset", "bind-organization", "bind-regulation", "bind-provision", "existing-target"} {
		t.Run(mode, func(t *testing.T) {
			x := in
			s := proto.Clone(source).(*pb.ExtractionBatch)
			d := proto.Clone(base.TargetDocument).(*pb.DocumentBatch)
			switch mode {
			case "mention":
				s.Mentions[0].Meta.RecordId = "mention:rerun"
				x.MentionID = "mention:rerun"
			case "model":
				s.Mentions[0].ExtractionManifest.Build = "model:rerun"
				s.Dependencies.ProducerManifest.Build = "model:rerun"
			case "parser":
				d.TextArtifacts[0].ParserManifest.Build = "parser:rerun"
			case "scope":
				x.Scope = "ID:secondary"
			case "type":
				s.Mentions[0].CandidateType = "activity"
				x.Policy.ScopesByType = map[string][]string{"activity": {"ID:national"}}
			case "chunk":
				d.Chunks[0].Meta.RecordId = "chunk:rerun"
			case "normalizer":
				d.TextArtifacts[0].NormalizerManifest = bindingProducer()
				d.TextArtifacts[0].NormalizerManifest.Build = "normalizer:new"
			case "text":
				raw := append([]byte(nil), x.Text.Bytes...)
				raw[len(raw)-1] = 'x'
				x.Text = sourcedAliasArtifact("text/plain;charset=utf-8", raw)
				d.TextArtifacts[0].NormalizedTextRef = x.Text.Ref
				for _, v := range d.Versions {
					v.TextRef = proto.Clone(x.Text.Ref).(*pb.ArtifactRef)
				}
			case "span":
				s.Mentions[0].TextSpan.EndByte--
				s.Mentions[0].SurfaceForm = s.Mentions[0].SurfaceForm[:len(s.Mentions[0].SurfaceForm)-1]
			case "source-ref-subset":
				// The fixture has another provision covering this span; if present,
				// the model may cite it as well without changing occurrence identity.
				for _, v := range d.Versions {
					if v.Meta.RecordId == s.Mentions[0].SourceRefs[0].ProvisionVersionId {
						continue
					}
					for _, span := range v.Spans {
						if span.TextArtifactId == s.Mentions[0].TextSpan.TextArtifactId && span.StartByte <= s.Mentions[0].TextSpan.StartByte && span.EndByte >= s.Mentions[0].TextSpan.EndByte {
							ref := proto.Clone(s.Mentions[0].SourceRefs[0]).(*pb.SourceVersionRef)
							ref.ProvisionVersionId = v.Meta.RecordId
							s.Mentions[0].SourceRefs = append(s.Mentions[0].SourceRefs, ref)
							break
						}
					}
				}
				if len(s.Mentions[0].SourceRefs) < 2 {
					t.Fatal("fixture must exercise a different valid source-ref subset")
				}
			case "bind-organization", "bind-regulation", "bind-provision":
				kind := strings.TrimPrefix(mode, "bind-")
				s.Mentions[0].CandidateType = kind
				x.Policy.ScopesByType = map[string][]string{kind: {"ID:national"}}
			case "existing-target":
				x.CanonicalID = "canonical:invented"
			}
			x.Document = sourcedAliasProto(t, domain.DocumentBatchMediaType, d)
			x.TargetDocument = x.Document
			s.SourceDocumentBatch = x.Document.Ref
			s.Dependencies.Dependencies[0].DependencyId = x.Document.Ref.ArtifactId
			s.Dependencies.Dependencies[0].Fingerprint = x.Document.Ref.ContentHash
			x.Source = sourcedAliasProto(t, domain.ExtractionBatchMediaType, s)
			p, e := domain.BuildSourcedAliasPreview(x)
			if strings.HasPrefix(mode, "bind-") || mode == "existing-target" {
				if e == nil {
					t.Fatal("invalid creation accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			stable := mode == "model" || mode == "mention" || mode == "chunk" || mode == "source-ref-subset"
			if (p.Registration.Entity.Meta.RecordId == baseID) != stable {
				t.Fatal("occurrence key stability mismatch", mode)
			}
			if p.PlanHash == base.PlanHash {
				t.Fatal("changed review evidence must require new approval")
			}
		})
	}
}
