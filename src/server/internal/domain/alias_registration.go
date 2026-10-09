// Builds an operator-reviewable alias from an authenticated EXTRACT occurrence and
// an independently selected BIND identity or reviewed provisional origin.
// It never infers the referent from the
// containing document or approves a legal equivalence. Explicit provisional mode
// proposes a source-occurrence identity; storage alone can allocate it atomically.
// Storage must authenticate registered artifacts/checkpoints and CAS the registry.
// Exact UTF-8 spans, policy scope and immutable profile pins bind the review hash.
// Bound source/context bytes and measure review/commit p95 and false links against
// configs/benchmark-targets.yaml; required quality/performance remain unmeasured.
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"math"
	"mime"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// AliasRegistration pairs a profile with an exact sourced alias. The identity
// must already exist unless a separate sourced creation review allocates it in
// the same transaction; the pair alone does not authorize a storage write.
type AliasRegistration struct {
	Entity *pb.CanonicalEntity
	Alias  *pb.Alias
}

type AliasSourceArtifact struct {
	Ref   *pb.ArtifactRef
	Bytes []byte
}

type SourcedAliasInput struct {
	TargetOrigin                                                     *ProvisionalAliasOrigin
	CreateProvisional                                                bool
	Corpus, AuthScope, MentionID, CanonicalID, Scope, PreferredLabel string
	ExpectedRevision                                                 uint64
	Source, Document, TargetDocument, Text                           AliasSourceArtifact
	Policy                                                           CandidatePlanningPolicy
	ExistingProfile                                                  *pb.CanonicalEntity
}

type SourcedAliasPreview struct {
	TargetOrigin          *SourcedAliasPreview
	TargetOriginOperation string
	CreateProvisional     bool
	LookupScopes          []RegistryLookupScope
	TargetDocument        *pb.DocumentBatch
	TargetDocumentRef     *pb.ArtifactRef
	ExpectedRevision      uint64
	Registration          AliasRegistration
	Mention               *pb.Mention
	Contexts              []*pb.TextItem
	SourceContext         *pb.RequestContext
	TargetDependencies    DocumentRegistryDependencies
	PlanHash              string
}

// BuildSourcedAliasPreview is shared by workflow inspection and storage admission.
// Hash-verified bytes still require DB membership checks before committing a review.
func BuildSourcedAliasPreview(in SourcedAliasInput) (*SourcedAliasPreview, error) {
	if !validDocumentID(in.Corpus) || !validDocumentID(in.MentionID) || (!in.CreateProvisional && !validDocumentID(in.CanonicalID)) ||
		in.AuthScope == "" || in.ExpectedRevision == 0 || in.ExpectedRevision >= math.MaxInt64 {
		return nil, errors.New("alias review requires corpus, scope, identities and registry revision")
	}
	if in.CreateProvisional && (in.CanonicalID != "" || in.ExistingProfile != nil || !proto.Equal(in.Document.Ref, in.TargetDocument.Ref)) {
		return nil, errors.New("provisional creation requires source inventory and no existing target/profile")
	}
	total := 0
	seen := map[string]*pb.ArtifactRef{}
	artifacts := []AliasSourceArtifact{in.Source, in.Document, in.TargetDocument, in.Text}
	if in.TargetOrigin != nil {
		artifacts = append(artifacts, in.TargetOrigin.Source, in.TargetOrigin.Text)
	}
	for _, a := range artifacts {
		if err := ValidateWire(a.Ref, DefaultWireLimits); err != nil {
			return nil, err
		}
		total += len(a.Bytes)
		if a.Ref.SchemaVersion != 1 || len(a.Bytes) == 0 || total > 64<<20 || a.Ref.ByteSize != uint64(len(a.Bytes)) ||
			a.Ref.ContentHash.Sha256 != aliasBytesHash(a.Bytes) {
			return nil, errors.New("alias source bytes or budget differ")
		}
		if old := seen[a.Ref.ArtifactId]; old != nil && !proto.Equal(old, a.Ref) {
			return nil, ErrPersistentIntegrity
		}
		seen[a.Ref.ArtifactId] = a.Ref
	}
	media, params, mediaErr := mime.ParseMediaType(in.Text.Ref.MediaType)
	if (in.Source.Ref.MediaType != ExtractionBatchMediaType && in.Source.Ref.MediaType != "application/x-protobuf") ||
		!IsDocumentBatchMediaType(in.Document.Ref.MediaType) || !IsDocumentBatchMediaType(in.TargetDocument.Ref.MediaType) ||
		mediaErr != nil || media != "text/plain" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || !utf8.Valid(in.Text.Bytes) {
		return nil, errors.New("alias artifact media or text encoding differs")
	}
	source, document, target := new(pb.ExtractionBatch), new(pb.DocumentBatch), new(pb.DocumentBatch)
	for _, v := range []struct {
		raw []byte
		msg proto.Message
	}{{in.Source.Bytes, source}, {in.Document.Bytes, document}, {in.TargetDocument.Bytes, target}} {
		if err := DecodeWire(v.raw, v.msg, DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	for _, c := range []*pb.RequestContext{source.Context, document.Context, target.Context} {
		if c.GetCorpusId() != in.Corpus || c.GetAuthScopeRef() != in.AuthScope {
			return nil, errors.New("alias source crosses corpus or authorization scope")
		}
	}
	if source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || !proto.Equal(source.SourceDocumentBatch, in.Document.Ref) {
		return nil, errors.New("alias requires complete extraction from the exact document")
	}
	if err := ValidateDocumentBatchClosure(document, 100000); err != nil {
		return nil, err
	}
	if err := ValidateExtractionBatchClosure(source, document, 100000); err != nil {
		return nil, err
	}
	dependencies, err := PlanDocumentRegistryDependencies(target, 100000)
	if err != nil {
		return nil, err
	}
	if dependencies.ObservedRevision > in.ExpectedRevision {
		return nil, errors.New("alias target is newer than inspected registry")
	}
	var mention *pb.Mention
	for _, m := range source.Mentions {
		if m.Meta.RecordId == in.MentionID {
			mention = m
		}
	}
	if mention == nil {
		return nil, errors.New("support mention is absent from extraction")
	}
	plans, err := PlanRegistryCandidates(source, in.Policy)
	if err != nil {
		return nil, err
	}
	allowed := false
	var lookupScopes []RegistryLookupScope
	for _, p := range plans {
		if p.MentionID == in.MentionID {
			lookupScopes = append(lookupScopes, p.Scopes...)
			for _, s := range p.Scopes {
				if s.CanonicalScope == in.Scope {
					allowed = true
				}
			}
		}
	}
	if !allowed {
		return nil, errors.New("alias scope is outside pinned candidate policy")
	}
	var originPreview *SourcedAliasPreview
	originOperation := ""
	if in.TargetOrigin != nil {
		if err := ValidateWire(in.ExistingProfile, DefaultWireLimits); err != nil {
			return nil, err
		}
		originPreview, err = buildAliasTargetPreview(in)
		if err != nil {
			return nil, err
		}
		originOperation = in.TargetOrigin.Ref.Operation
	}
	var identity *DocumentRegistryIdentity
	for i := range dependencies.Identities {
		if dependencies.Identities[i].CanonicalID == in.CanonicalID {
			identity = &dependencies.Identities[i]
		}
	}
	if originPreview != nil {
		e := originPreview.Registration.Entity
		identity = &DocumentRegistryIdentity{CanonicalID: e.Meta.RecordId, EntityType: CanonicalEntityTypeCode(e.EntityType), IdentityScope: e.IdentityKeys[0].Namespace, IdentityKey: e.IdentityKeys[0].Value}
	}
	canonicalID, profileRevision := in.CanonicalID, in.ExpectedRevision
	if in.CreateProvisional {
		identity, err = provisionalSourceIdentity(in, document, mention)
		if err != nil {
			return nil, err
		}
		canonicalID, profileRevision = identity.CanonicalID, in.ExpectedRevision+1
	}
	if identity == nil || identity.EntityType != CanonicalEntityTypeCode(mention.CandidateType) {
		return nil, errors.New("selected BIND identity is absent or wrong type")
	}
	entity := &pb.CanonicalEntity{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: in.Corpus, RecordId: canonicalID},
		EntityType: mention.CandidateType, Scope: in.Scope, PreferredLabel: in.PreferredLabel,
		IdentityKeys:     []*pb.IdentityKey{{Namespace: identity.IdentityScope, Value: identity.IdentityKey}},
		RegistryRevision: profileRevision, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}
	if in.ExistingProfile != nil {
		entity = proto.Clone(in.ExistingProfile).(*pb.CanonicalEntity)
		keyFound := false
		for _, k := range entity.IdentityKeys {
			if k.Namespace == identity.IdentityScope && k.Value == identity.IdentityKey {
				keyFound = true
			}
		}
		if entity.GetMeta().GetCorpusId() != in.Corpus || entity.GetMeta().GetRecordId() != in.CanonicalID || entity.EntityType != mention.CandidateType ||
			entity.Scope != in.Scope || entity.PreferredLabel != in.PreferredLabel || entity.RegistryRevision > in.ExpectedRevision || !keyFound {
			return nil, errors.New("existing profile cannot be changed through alias registration")
		}
	}
	if err := ValidateWire(entity, DefaultWireLimits); err != nil {
		return nil, err
	}
	if entity.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED || entity.Meta.Visibility != nil {
		return nil, errors.New("alias bootstrap requires immutable unreviewed profile")
	}
	span := mention.TextSpan
	var text *pb.TextArtifact
	for _, t := range document.TextArtifacts {
		if t.Meta.RecordId == span.TextArtifactId {
			text = t
		}
	}
	if text == nil || !proto.Equal(text.NormalizedTextRef, in.Text.Ref) || span.StartByte >= span.EndByte || span.EndByte > uint64(len(in.Text.Bytes)) ||
		string(in.Text.Bytes[span.StartByte:span.EndByte]) != mention.SurfaceForm {
		return nil, errors.New("alias surface differs from authenticated source text")
	}
	contexts := []*pb.TextItem{}
	versions := map[string]*pb.ProvisionVersion{}
	provisions := map[string]*pb.Provision{}
	for _, v := range document.Versions {
		versions[v.Meta.RecordId] = v
	}
	for _, p := range document.Provisions {
		provisions[p.Meta.RecordId] = p
	}
	contextBytes := 0
	for _, chunk := range document.Chunks {
		cs := chunk.TextSpan
		if cs.TextArtifactId != span.TextArtifactId || cs.StartByte > span.StartByte || cs.EndByte < span.EndByte {
			continue
		}
		if cs.StartByte >= cs.EndByte || cs.EndByte > uint64(len(in.Text.Bytes)) || !utf8.Valid(in.Text.Bytes[cs.StartByte:cs.EndByte]) {
			return nil, errors.New("invalid alias context range")
		}
		contextBytes += int(cs.EndByte - cs.StartByte)
		if contextBytes > 4<<20 {
			return nil, errors.New("alias context budget exceeded")
		}
		refs := []*pb.SourceVersionRef{}
		for _, id := range chunk.ProvisionVersionRefs {
			v := versions[id]
			if v == nil || provisions[v.ProvisionId] == nil {
				return nil, errors.New("alias chunk version is not bound")
			}
			refs = append(refs, &pb.SourceVersionRef{SourceBlobId: text.SourceBlobId, ProvisionVersionId: id, RegulationId: provisions[v.ProvisionId].RegulationId})
		}
		contexts = append(contexts, &pb.TextItem{ItemId: chunk.Meta.RecordId, Text: string(in.Text.Bytes[cs.StartByte:cs.EndByte]),
			Provenance: &pb.Provenance{Sources: refs, Spans: []*pb.TextSpan{proto.Clone(cs).(*pb.TextSpan)}}})
	}
	if len(contexts) == 0 {
		return nil, errors.New("alias mention has no structural context")
	}
	normalized, err := NormalizeCandidateSurface(mention.SurfaceForm)
	if err != nil {
		return nil, err
	}
	id := sha256.New()
	for _, s := range []string{"sourced-alias:v1", in.Corpus, canonicalID, in.Scope, in.MentionID, in.Source.Ref.ContentHash.Sha256} {
		aliasHashPart(id, []byte(s))
	}
	alias := &pb.Alias{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: in.Corpus, RecordId: "alias:sourced:" + hex.EncodeToString(id.Sum(nil))},
		CanonicalId: canonicalID, Surface: mention.SurfaceForm, NormalizedLookup: normalized, Language: "und", Scope: in.Scope, SupportRefs: []string{in.MentionID}}
	if err = ValidateWire(alias, DefaultWireLimits); err != nil {
		return nil, err
	}
	policyHash, _ := in.Policy.Fingerprint()
	planHash, err := sourcedAliasPlanHash(in, entity, alias, policyHash.Sha256, originPreview, originOperation)
	if err != nil {
		return nil, err
	}
	return &SourcedAliasPreview{TargetOrigin: originPreview, TargetOriginOperation: originOperation, CreateProvisional: in.CreateProvisional, LookupScopes: lookupScopes, TargetDocument: target, TargetDocumentRef: proto.Clone(in.TargetDocument.Ref).(*pb.ArtifactRef), ExpectedRevision: in.ExpectedRevision, Registration: AliasRegistration{Entity: entity, Alias: alias}, Mention: proto.Clone(mention).(*pb.Mention),
		Contexts: contexts, SourceContext: proto.Clone(source.Context).(*pb.RequestContext), TargetDependencies: dependencies, PlanHash: planHash}, nil
}

func sourcedAliasPlanHash(in SourcedAliasInput, entity *pb.CanonicalEntity, alias *pb.Alias, policyHash string, originPreview *SourcedAliasPreview, originOperation string) (string, error) {
	h := sha256.New()
	version := "sourced-alias-review:v1"
	if in.CreateProvisional {
		version = "sourced-provisional-review:v1"
	}
	if originPreview != nil {
		version = "sourced-provisional-alias-review:v1"
	}
	aliasHashPart(h, []byte(version))
	if originPreview != nil {
		aliasHashPart(h, []byte(originOperation))
		aliasHashPart(h, []byte(originPreview.PlanHash))
	}
	var revision [8]byte
	binary.BigEndian.PutUint64(revision[:], in.ExpectedRevision)
	aliasHashPart(h, revision[:])
	for _, s := range []string{in.Corpus, in.AuthScope, policyHash} {
		aliasHashPart(h, []byte(s))
	}
	for _, msg := range []proto.Message{in.Source.Ref, in.Document.Ref, in.TargetDocument.Ref, in.Text.Ref, entity, alias} {
		raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(msg)
		if err != nil {
			return "", err
		}
		aliasHashPart(h, raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func aliasBytesHash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func aliasHashPart(h hash.Hash, raw []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(raw)))
	h.Write(n[:])
	h.Write(raw)
}
