// Inspects exact-source aliases and commits only an explicitly approved preview.
// This operator workflow owns bounded file hydration; PostgreSQL owns source
// authority, profile/alias persistence and revision CAS. It does not infer a
// mention's target from provenance or automatically accept model suggestions.
// Reuse source bytes within the operation and measure hydration/commit p95 and
// false links separately; required benchmark targets remain unmeasured.
package workflows

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type SourcedAliasStore interface {
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
	LoadAliasProfile(context.Context, string, string, uint64) (*pb.CanonicalEntity, uint64, error)
	LoadResolutionEvidenceSources(context.Context, *pb.RequestContext, []string, int) ([]*pb.ArtifactRef, error)
	VerifyDocumentRegistryView(context.Context, string, *pb.ArtifactRef, []byte, uint64, int) error
	RegisterSourcedAlias(context.Context, domain.SourcedAliasInput, string, string, string, string) (uint64, error)
}

type SourcedAliasOptions struct {
	Corpus, AuthScope, SourceArtifactID, TargetDocumentID, MentionID, CanonicalID, Scope, PreferredLabel string
	Revision                                                                                             uint64
	Policy                                                                                               domain.CandidatePlanningPolicy
}

// Inspection owns its exact source inputs so a caller cannot change them through
// the display object after approval. Accept still rebuilds the shared validator.
type SourcedAliasInspection struct {
	input   domain.SourcedAliasInput
	preview *domain.SourcedAliasPreview
}

func (v *SourcedAliasInspection) Preview() (*domain.SourcedAliasPreview, error) {
	if v == nil {
		return nil, errors.New("alias inspection is missing")
	}
	return domain.BuildSourcedAliasPreview(v.input)
}

func InspectSourcedAlias(ctx context.Context, store SourcedAliasStore, files SemanticResolutionArtifactReader, o SourcedAliasOptions) (*SourcedAliasInspection, error) {
	if ctx == nil || store == nil || files == nil || o.Corpus == "" || o.AuthScope == "" || o.TargetDocumentID == "" {
		return nil, errors.New("configured source and target stores required")
	}
	remaining := uint64(64 << 20)
	loaded := map[string]domain.AliasSourceArtifact{}
	read := func(ref *pb.ArtifactRef) (domain.AliasSourceArtifact, error) {
		if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
			return domain.AliasSourceArtifact{}, err
		}
		if previous, ok := loaded[ref.ArtifactId]; ok {
			if !proto.Equal(previous.Ref, ref) {
				return domain.AliasSourceArtifact{}, domain.ErrPersistentIntegrity
			}
			return previous, nil
		}
		if ref.ByteSize == 0 || ref.ByteSize > remaining {
			return domain.AliasSourceArtifact{}, errors.New("alias artifact budget exceeded")
		}
		raw, err := files.ReadVerified(ctx, ref, remaining)
		if err != nil {
			return domain.AliasSourceArtifact{}, err
		}
		if uint64(len(raw)) != ref.ByteSize {
			return domain.AliasSourceArtifact{}, domain.ErrPersistentIntegrity
		}
		remaining -= ref.ByteSize
		a := domain.AliasSourceArtifact{Ref: proto.Clone(ref).(*pb.ArtifactRef), Bytes: raw}
		loaded[ref.ArtifactId] = a
		return a, nil
	}
	ref, err := store.LoadArtifact(ctx, o.Corpus, o.SourceArtifactID)
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
	refs, err := store.LoadResolutionEvidenceSources(ctx, extraction.Context, []string{o.MentionID}, 100000)
	if err != nil {
		return nil, err
	}
	found := false
	for _, r := range refs {
		if proto.Equal(r, ref) {
			found = true
		}
	}
	if !found {
		return nil, errors.New("alias source is not a successful catalogued extraction")
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
		if m.GetMeta().GetRecordId() == o.MentionID {
			for _, t := range doc.TextArtifacts {
				if t.Meta.RecordId == m.TextSpan.TextArtifactId {
					textRef = t.NormalizedTextRef
				}
			}
		}
	}
	if textRef == nil {
		return nil, errors.New("support text is absent")
	}
	text, err := read(textRef)
	if err != nil {
		return nil, err
	}
	targetRef, err := store.LoadArtifact(ctx, o.Corpus, o.TargetDocumentID)
	if err != nil {
		return nil, err
	}
	target, err := read(targetRef)
	if err != nil {
		return nil, err
	}
	profile, revision, err := store.LoadAliasProfile(ctx, o.Corpus, o.CanonicalID, o.Revision)
	if err != nil {
		return nil, err
	}
	if err = store.VerifyDocumentRegistryView(ctx, o.Corpus, target.Ref, target.Bytes, revision, 100000); err != nil {
		return nil, err
	}
	// Clone policy maps through their JSON representation is unnecessary: build a
	// fresh bounded map here, preserving explicit false entries in the fingerprint.
	policy := o.Policy
	policy.ScopesByType = make(map[string][]string, len(o.Policy.ScopesByType))
	for k, v := range o.Policy.ScopesByType {
		policy.ScopesByType[k] = append([]string(nil), v...)
	}
	if o.Policy.IncludeSourceRegulationType != nil {
		policy.IncludeSourceRegulationType = make(map[string]bool, len(o.Policy.IncludeSourceRegulationType))
		for k, v := range o.Policy.IncludeSourceRegulationType {
			policy.IncludeSourceRegulationType[k] = v
		}
	}
	in := domain.SourcedAliasInput{Corpus: o.Corpus, AuthScope: o.AuthScope, MentionID: o.MentionID, CanonicalID: o.CanonicalID, Scope: o.Scope, PreferredLabel: o.PreferredLabel,
		ExpectedRevision: revision, Source: source, Document: document, TargetDocument: target, Text: text, Policy: policy, ExistingProfile: profile}
	preview, err := domain.BuildSourcedAliasPreview(in)
	if err != nil {
		return nil, err
	}
	return &SourcedAliasInspection{input: in, preview: preview}, nil
}

func (v *SourcedAliasInspection) Accept(ctx context.Context, store SourcedAliasStore, operation, hash, actor, reason string) (uint64, error) {
	if v == nil || store == nil || hash != v.preview.PlanHash {
		return 0, errors.New("approval hash differs from alias inspection")
	}
	return store.RegisterSourcedAlias(ctx, v.input, operation, hash, actor, reason)
}
