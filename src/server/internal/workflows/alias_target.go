// Hydrates the original reviewed occurrence for an existing provisional target.
// The parent inspection supplies its shared hash/byte budget and cache. The
// catalog must expose the target evidence in the same authorization scope before
// text is displayed; storage rechecks the receipt and rows inside commit.
// No LLM is called. Measure hydration bytes/p95 and false links on reviewed data;
// benchmark-targets.yaml remains the unmeasured acceptance source.
package workflows

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func hydrateAliasTarget(ctx context.Context, store SourcedAliasStore, read func(*pb.ArtifactRef) (domain.AliasSourceArtifact, error), o SourcedAliasOptions, revision uint64) (*domain.ProvisionalAliasOrigin, error) {
	origin, err := store.LoadProvisionalAliasOrigin(ctx, o.Corpus, o.CanonicalID, o.AuthScope, revision)
	if err != nil {
		return nil, err
	}
	ref, err := store.LoadArtifact(ctx, o.Corpus, origin.SourceArtifactID)
	if err != nil {
		return nil, err
	}
	source, err := read(ref)
	if err != nil {
		return nil, err
	}
	extraction := new(pb.ExtractionBatch)
	if err = domain.DecodeWire(source.Bytes, extraction, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if extraction.Context.GetCorpusId() != o.Corpus || extraction.Context.GetAuthScopeRef() != o.AuthScope {
		return nil, domain.ErrPersistentIntegrity
	}
	refs, err := store.LoadResolutionEvidenceSources(ctx, extraction.Context, []string{origin.MentionID}, 100000)
	if err != nil {
		return nil, err
	}
	found := false
	for _, r := range refs {
		if proto.Equal(r, source.Ref) {
			found = true
		}
	}
	if !found {
		return nil, errors.New("target occurrence is not catalogued")
	}
	document, err := read(extraction.SourceDocumentBatch)
	if err != nil {
		return nil, err
	}
	doc := new(pb.DocumentBatch)
	if err = domain.DecodeWire(document.Bytes, doc, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	var textRef *pb.ArtifactRef
	for _, m := range extraction.Mentions {
		if m.Meta.RecordId == origin.MentionID {
			for _, text := range doc.TextArtifacts {
				if text.Meta.RecordId == m.TextSpan.TextArtifactId {
					textRef = text.NormalizedTextRef
				}
			}
		}
	}
	if textRef == nil {
		return nil, errors.New("target occurrence text missing")
	}
	text, err := read(textRef)
	if err != nil {
		return nil, err
	}
	return &domain.ProvisionalAliasOrigin{Ref: origin, Source: source, Document: document, Text: text}, nil
}
