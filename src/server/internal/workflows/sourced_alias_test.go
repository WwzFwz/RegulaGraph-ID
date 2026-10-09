// Exercises source-backed alias inspection with actual BIND materialization and
// exact EXTRACT/text fixtures. Negative cases protect source/scope/type/approval
// integrity; database tests separately prove atomic writes and consumer hydration.
// These synthetic mentions are test data, not production extraction or gold labels.
package workflows

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func sourcedAliasFixture(t *testing.T, store BindingExecutionStore) (domain.SourcedAliasInput, *pb.ExtractionBatch, *pb.IngestionRequest) {
	t.Helper()
	ctx := context.Background()
	doc := bindingWorkflowBatch(true)
	text := []byte("Kementerian Contoh" + strings.Repeat(" ", 100-len("Kementerian Contoh")))
	textArtifact := sourcedAliasArtifact("text/plain;charset=utf-8", text)
	doc.TextArtifacts[0].NormalizedTextRef = textArtifact.Ref
	fakeFiles := &bindingArtifactsFake{}
	executor, err := NewBindingExecutor(store, fakeFiles, bindingWorkflowConfig())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := proto.Marshal(doc)
	bound, revision, err := executor.bind(ctx, domain.JobRecord{JobID: "job:alias-bind", CorpusID: doc.Meta.CorpusId}, fmt.Sprintf("%x", sha256.Sum256(raw)), doc)
	if err != nil {
		t.Fatal(err)
	}
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: doc.Meta.CorpusId, RecordId: id}
	}
	var rootVersion string
	for _, v := range bound.Versions {
		if v.Spans[0].StartByte == 0 {
			rootVersion = v.Meta.RecordId
		}
	}
	bound.Chunks = []*pb.Chunk{{Meta: meta("chunk:alias"), ProvisionVersionRefs: []string{rootVersion}, TextSpan: &pb.TextSpan{TextArtifactId: "text:a", EndByte: 100}, StructureNodeRefs: []string{"structure:root"}, ChunkerManifest: bindingProducer(), TokenCounts: []*pb.TokenUsage{{InputTokens: 10, TokenizerId: "tokenizer:fixture"}}}}
	document := sourcedAliasProto(t, domain.DocumentBatchMediaType, bound)
	source := extractionBatchFixture(doc.Meta.CorpusId, document.Ref)
	source.Context = proto.Clone(bound.Context).(*pb.RequestContext)
	source.Mentions = []*pb.Mention{{Meta: meta("mention:alias"), SurfaceForm: "Kementerian Contoh", CandidateType: "organization",
		TextSpan:           &pb.TextSpan{TextArtifactId: "text:a", EndByte: uint64(len("Kementerian Contoh"))},
		SourceRefs:         []*pb.SourceVersionRef{{SourceBlobId: bound.Sources[0].Meta.RecordId, RegulationId: bound.Regulations[0].Meta.RecordId, ProvisionVersionId: rootVersion}},
		ExtractionManifest: proto.Clone(source.Dependencies.ProducerManifest).(*pb.ProducerManifest)}}
	extraction := sourcedAliasProto(t, domain.ExtractionBatchMediaType, source)
	policy := domain.CandidatePlanningPolicy{ScopesByType: map[string][]string{"organization": {"ID:national"}}, MaximumMentions: 100, MaximumScopesPerMention: 2, MaximumTotalScopes: 200}
	pin, _ := policy.Fingerprint()
	request := &pb.IngestionRequest{CorpusId: doc.Meta.CorpusId, Operation: pb.JobOperation_JOB_OPERATION_INGEST, IdempotencyKey: "ingest:alias",
		Sources:        []*pb.SourceLocator{{PortalId: "bpk", Locator: &pb.SourceLocator_Blob{Blob: bound.Sources[0].ArtifactRef}}},
		ConfigManifest: &pb.ProducerManifest{Software: "test", Build: "fixture", SchemaVersion: 1, ConfigHash: source.Context.ConfigFingerprint, InputHashes: []*pb.ContentHash{pin}}}
	if err = domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		t.Fatal("invalid fixture request", err)
	}
	return domain.SourcedAliasInput{Corpus: doc.Meta.CorpusId, AuthScope: doc.Context.AuthScopeRef, MentionID: "mention:alias", CanonicalID: bound.Regulations[0].IssuerId, Scope: "ID:national", PreferredLabel: "Kementerian Contoh",
		ExpectedRevision: revision, Source: extraction, Document: document, TargetDocument: document, Text: textArtifact, Policy: policy}, source, request
}

func sourcedAliasArtifact(media string, raw []byte) domain.AliasSourceArtifact {
	h := fmt.Sprintf("%x", sha256.Sum256(raw))
	return domain.AliasSourceArtifact{Bytes: raw, Ref: &pb.ArtifactRef{ArtifactId: "artifact:" + h, ContentHash: &pb.ContentHash{Sha256: h}, StorageKey: "alias/" + h, MediaType: media, ByteSize: uint64(len(raw)), SchemaVersion: 1}}
}
func sourcedAliasProto(t *testing.T, media string, m proto.Message) domain.AliasSourceArtifact {
	t.Helper()
	raw, e := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	return sourcedAliasArtifact(media, raw)
}

func TestSourcedAliasPreviewExactSourceAndTarget(t *testing.T) {
	in, _, _ := sourcedAliasFixture(t, &bindingStoreFake{})
	p, err := domain.BuildSourcedAliasPreview(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Registration.Alias.Surface != "Kementerian Contoh" || p.Registration.Alias.NormalizedLookup != "kementerian contoh" || p.Registration.Alias.CanonicalId != in.CanonicalID || p.Mention.SourceRefs[0].RegulationId == in.CanonicalID || len(p.Contexts) != 1 || len(p.Contexts[0].Provenance.Sources) != 1 || p.TargetDocument == nil {
		t.Fatal("source/target evidence lost")
	}
	p2, err := domain.BuildSourcedAliasPreview(in)
	if err != nil || p.PlanHash != p2.PlanHash {
		t.Fatal("nondeterministic preview", err)
	}
	p.Registration.Entity.PreferredLabel = "changed display"
	p3, _ := domain.BuildSourcedAliasPreview(in)
	if p3.PlanHash != p2.PlanHash {
		t.Fatal("preview mutated input")
	}
	for name, mutate := range map[string]func(*domain.SourcedAliasInput){
		"foreign corpus":        func(x *domain.SourcedAliasInput) { x.Corpus = "corpus:other" },
		"foreign authorization": func(x *domain.SourcedAliasInput) { x.AuthScope = "scope:other" },
		"scope outside policy":  func(x *domain.SourcedAliasInput) { x.Scope = "ID:regional" },
		"missing target":        func(x *domain.SourcedAliasInput) { x.CanonicalID = "canonical:absent" },
		"wrong type":            func(x *domain.SourcedAliasInput) { x.CanonicalID = p3.TargetDocument.Regulations[0].Meta.RecordId },
		"missing mention":       func(x *domain.SourcedAliasInput) { x.MentionID = "mention:absent" },
		"stale registry":        func(x *domain.SourcedAliasInput) { x.ExpectedRevision = 1 },
		"corrupt bytes":         func(x *domain.SourcedAliasInput) { x.Text.Bytes = []byte("different") },
		"surface mismatch with valid hash": func(x *domain.SourcedAliasInput) {
			d := proto.Clone(p3.TargetDocument).(*pb.DocumentBatch)
			x.Text = sourcedAliasArtifact("text/plain", []byte(strings.Repeat("x", 100)))
			d.TextArtifacts[0].NormalizedTextRef = x.Text.Ref
			for _, v := range d.Versions {
				v.TextRef = proto.Clone(x.Text.Ref).(*pb.ArtifactRef)
			}
			x.Document = sourcedAliasProto(t, domain.DocumentBatchMediaType, d)
			s := new(pb.ExtractionBatch)
			_ = proto.Unmarshal(x.Source.Bytes, s)
			s.SourceDocumentBatch = x.Document.Ref
			s.Dependencies.Dependencies[0].DependencyId = x.Document.Ref.ArtifactId
			s.Dependencies.Dependencies[0].Fingerprint = x.Document.Ref.ContentHash
			x.Source = sourcedAliasProto(t, domain.ExtractionBatchMediaType, s)
		},
	} {
		t.Run(name, func(t *testing.T) {
			x := in
			mutate(&x)
			if _, e := domain.BuildSourcedAliasPreview(x); e == nil {
				t.Fatal("invalid alias accepted")
			} else if name == "surface mismatch with valid hash" && !strings.Contains(e.Error(), "surface differs") {
				t.Fatalf("fixture failed before exact-source comparison: %v", e)
			}
		})
	}
}
