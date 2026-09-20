// Verifies registry-bound document materialization without asserting legal truth.
// Tests cover exact structural identity, source/version closure, deterministic replay, stale plans,
// unknown temporal status, and conservative handling of unbound sources. Performance gates remain
// REQUIRED_UNMEASURED and require the corpus benchmark suite.
package domain

import (
	"reflect"
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func TestBindDocumentBatchMaterializesRegistryOwnedRecords(t *testing.T) {
	batch, binding := bindingFixture()
	candidates, err := PlanProvisionIdentities(batch, []RegulationDocumentBinding{binding}, 100)
	if err != nil {
		t.Fatal(err)
	}
	assignments := provisionAssignments(t, candidates, 8)
	config := bindingConfig(8)
	first, err := BindDocumentBatch(batch, []RegulationDocumentBinding{binding}, candidates, assignments, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BindDocumentBatch(batch, []RegulationDocumentBinding{binding}, candidates, assignments, config)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("binding replay drifted: err=%v", err)
	}
	if len(first.Regulations) != 1 || len(first.Editions) != 1 || len(first.Provisions) != 2 || len(first.Versions) != 2 {
		t.Fatalf("unexpected bound record counts: regulations=%d editions=%d provisions=%d versions=%d",
			len(first.Regulations), len(first.Editions), len(first.Provisions), len(first.Versions))
	}
	if first.Regulations[0].IssuerId != binding.IssuerID || first.Regulations[0].IdentityStatus != pb.IdentityStatus_IDENTITY_STATUS_UNRESOLVED {
		t.Fatalf("regulation overclaimed identity: %#v", first.Regulations[0])
	}
	if !reflect.DeepEqual(first.Editions[0].SourceRefs, binding.Candidate.ObservationIDs) {
		t.Fatalf("edition lost observation provenance: %#v", first.Editions[0].SourceRefs)
	}
	for _, version := range first.Versions {
		if version.LegalStatus != pb.LegalStatus_LEGAL_STATUS_UNKNOWN ||
			version.GetLegalInterval().GetStart().GetKnowledge() != pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN ||
			version.GetReviewState() != pb.ReviewState_REVIEW_STATE_UNREVIEWED {
			t.Fatalf("version invented temporal certainty: %#v", version)
		}
	}
	if first.GetDependencyManifest().GetLookupScopeRevisions()[0].GetScopeId() != "canonical-registry" ||
		first.GetDependencyManifest().GetLookupScopeRevisions()[0].GetRevision() != 8 {
		t.Fatalf("registry revision dependency missing: %#v", first.GetDependencyManifest().GetLookupScopeRevisions())
	}
	if err = ValidateWire(first, config.WireLimits); err != nil {
		t.Fatalf("bound batch is not wire-valid: %v", err)
	}
}

func TestProvisionIdentitySeparatesCanonicalRegulations(t *testing.T) {
	batch, binding := bindingFixture()
	first, err := PlanProvisionIdentities(batch, []RegulationDocumentBinding{binding}, 100)
	if err != nil {
		t.Fatal(err)
	}
	other := binding
	other.RegulationID = "canonical:regulation:other"
	second, err := PlanProvisionIdentities(batch, []RegulationDocumentBinding{other}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) || first[1].IdentityKey == second[1].IdentityKey {
		t.Fatal("provision identity did not remain scoped to canonical regulation")
	}
}

func TestBindDocumentBatchRejectsStalePlanAndMissingAssignments(t *testing.T) {
	batch, binding := bindingFixture()
	candidates, err := PlanProvisionIdentities(batch, []RegulationDocumentBinding{binding}, 100)
	if err != nil {
		t.Fatal(err)
	}
	assignments := provisionAssignments(t, candidates, 8)
	stale := append([]ProvisionIdentityCandidate(nil), candidates...)
	stale[0].StructuralPath = append([]string(nil), stale[0].StructuralPath...)
	stale[0].StructuralPath[0] = "changed"
	if _, err = BindDocumentBatch(batch, []RegulationDocumentBinding{binding}, stale, assignments, bindingConfig(8)); err == nil {
		t.Fatal("stale provision plan was accepted")
	}
	if _, err = BindDocumentBatch(batch, []RegulationDocumentBinding{binding}, candidates, assignments[:1], bindingConfig(8)); err == nil {
		t.Fatal("partial provision assignment set was accepted")
	}
}

func TestPlanProvisionIdentitiesRejectsStaleObservationsAndInvalidStructure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*pb.DocumentBatch, *RegulationDocumentBinding)
	}{
		{"unknown observation provenance", func(_ *pb.DocumentBatch, binding *RegulationDocumentBinding) {
			binding.Candidate.ObservationIDs = []string{"observation:unknown"}
			refreshBindingClaims(binding)
		}},
		{"changed title", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			setObservationText(batch.Observations[0], "page_title", "Judul berubah")
		}},
		{"changed official number", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			setObservationText(batch.Observations[0], "number", "2")
		}},
		{"missing structures", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			batch.Structures = nil
		}},
		{"child outside parent", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			batch.Structures[0].SourceSpans[0].EndByte = 50
		}},
		{"second document root", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			root := proto.Clone(batch.Structures[0]).(*pb.StructureNode)
			root.Meta.RecordId = "structure:root-2"
			root.Label = "document copy"
			root.OrderedChildren = nil
			batch.Structures = append(batch.Structures, root)
		}},
		{"overlapping node spans", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			batch.Structures[1].SourceSpans = append(batch.Structures[1].SourceSpans,
				&pb.TextSpan{TextArtifactId: "text:a", StartByte: 50, EndByte: 70})
		}},
		{"text artifact without structure", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			artifact := proto.Clone(batch.TextArtifacts[0]).(*pb.TextArtifact)
			artifact.Meta.RecordId = "text:b"
			artifact.RawTextRef.ArtifactId, artifact.RawTextRef.StorageKey = "raw:b", "raw/b.txt"
			artifact.NormalizedTextRef.ArtifactId, artifact.NormalizedTextRef.StorageKey = "normalized:b", "normalized/b.txt"
			artifact.MappingRef.ArtifactId, artifact.MappingRef.StorageKey = "mapping:b", "mapping/b.bin"
			artifact.PageResults[0].Spans[0].TextArtifactId = "text:b"
			batch.TextArtifacts = append(batch.TextArtifacts, artifact)
		}},
		{"overlapping siblings", func(batch *pb.DocumentBatch, _ *RegulationDocumentBinding) {
			rootID := batch.Structures[0].GetMeta().GetRecordId()
			batch.Structures[0].OrderedChildren = append(batch.Structures[0].OrderedChildren, "structure:article-2")
			batch.Structures = append(batch.Structures, &pb.StructureNode{
				Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "structure:article-2"},
				Kind: pb.StructureKind_STRUCTURE_KIND_ARTICLE, Label: "Pasal 2", ParentId: &rootID,
				SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:a", StartByte: 50, EndByte: 90}},
			})
		}},
		{"cycle on unbound source", func(batch *pb.DocumentBatch, binding *RegulationDocumentBinding) {
			*binding = RegulationDocumentBinding{}
			rootID := batch.Structures[0].GetMeta().GetRecordId()
			articleID := batch.Structures[1].GetMeta().GetRecordId()
			batch.Structures[0].ParentId = &articleID
			batch.Structures[0].OrderedChildren = []string{articleID}
			batch.Structures[0].SourceSpans[0].StartByte = 10
			batch.Structures[1].OrderedChildren = []string{rootID}
			batch.Structures[1].SourceSpans[0].StartByte = 10
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch, binding := bindingFixture()
			test.mutate(batch, &binding)
			bindings := []RegulationDocumentBinding{binding}
			if binding.RegulationID == "" {
				bindings = nil
			}
			if _, err := PlanProvisionIdentities(batch, bindings, 100); err == nil {
				t.Fatal("invalid or stale binding input was accepted")
			}
		})
	}
}

func TestBindDocumentBatchIdentityIncludesProducerConfigAndIssuerDependency(t *testing.T) {
	batch, binding := bindingFixture()
	candidates, err := PlanProvisionIdentities(batch, []RegulationDocumentBinding{binding}, 100)
	if err != nil {
		t.Fatal(err)
	}
	assignments := provisionAssignments(t, candidates, 8)
	idConfig := bindingConfig(8)
	indonesian, err := BindDocumentBatch(batch, []RegulationDocumentBinding{binding}, candidates, assignments, idConfig)
	if err != nil {
		t.Fatal(err)
	}
	enConfig := idConfig
	enConfig.Language = "en"
	english, err := BindDocumentBatch(batch, []RegulationDocumentBinding{binding}, candidates, assignments, enConfig)
	if err != nil {
		t.Fatal(err)
	}
	if indonesian.GetMeta().GetRecordId() == english.GetMeta().GetRecordId() {
		t.Fatal("producer config change reused the bound batch identity")
	}
	foundIssuer := false
	for _, dependency := range indonesian.GetDependencyManifest().GetDependencies() {
		if dependency.DependencyId == binding.IssuerID {
			foundIssuer = true
		}
	}
	if !foundIssuer {
		t.Fatal("canonical issuer was not retained as an external dependency")
	}
}

func TestBindDocumentBatchMarksBoundSourceWithoutStructuredTextPartial(t *testing.T) {
	batch, firstBinding := bindingFixture()
	batch.Sources = append(batch.Sources, &pb.SourceBlob{
		Meta:      &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "source-blob:b"},
		RawSha256: fixtureHash("f"), MediaType: "application/pdf", ByteSize: 1,
		ArtifactRef: bindingArtifact("artifact:b", "objects/b.pdf", fixtureHash("f"), 1),
	})
	batch.Observations = append(batch.Observations, identityObservation("observation:b", "source-blob:b", map[string][]string{
		"regulation_type": {"Peraturan"}, "number": {"2"}, "year": {"2026"},
		"issuer": {"Kementerian Contoh"}, "page_title": {"Peraturan Kedua"},
	}))
	plan, err := PlanRegulationIdentities(batch, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 100})
	if err != nil {
		t.Fatal(err)
	}
	bindings := []RegulationDocumentBinding{firstBinding}
	for _, candidate := range plan.Candidates {
		if candidate.SourceBlobID != "source-blob:b" {
			continue
		}
		second := RegulationDocumentBinding{
			Candidate: candidate, RegulationID: "canonical:regulation:b",
			IssuerID: "canonical:organization:a", RegistryRevision: 7,
		}
		refreshBindingClaims(&second)
		bindings = append(bindings, second)
	}
	if len(bindings) != 2 {
		t.Fatal("second source did not produce a regulation binding")
	}
	candidates, err := PlanProvisionIdentities(batch, bindings, 100)
	if err != nil {
		t.Fatal(err)
	}
	output, err := BindDocumentBatch(batch, bindings, candidates, provisionAssignments(t, candidates, 8), bindingConfig(8))
	if err != nil {
		t.Fatal(err)
	}
	if output.Completeness != pb.Completeness_COMPLETENESS_PARTIAL {
		t.Fatalf("missing structured text was published as %s", output.Completeness)
	}
	found := false
	for _, issue := range output.Issues {
		if issue.Code == "STRUCTURED_TEXT_MISSING" && issue.RecordId == "source-blob:b" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing structured text did not emit a source-bound issue")
	}
}

func TestPlanProvisionIdentitiesRejectsAmbiguousPathAndSkipsUnboundSource(t *testing.T) {
	batch, binding := bindingFixture()
	duplicate := proto.Clone(batch.Structures[0]).(*pb.StructureNode)
	duplicate.Meta.RecordId = "structure:duplicate-root"
	batch.Structures = append(batch.Structures, duplicate)
	if _, err := PlanProvisionIdentities(batch, []RegulationDocumentBinding{binding}, 100); err == nil {
		t.Fatal("ambiguous structural path was accepted")
	}

	batch, _ = bindingFixture()
	planned, err := PlanProvisionIdentities(batch, nil, 100)
	if err != nil || len(planned) != 0 {
		t.Fatalf("unbound source did not remain for review: plan=%#v err=%v", planned, err)
	}
}

func bindingFixture() (*pb.DocumentBatch, RegulationDocumentBinding) {
	observation := identityObservation("observation:a", "source-blob:a", map[string][]string{
		"regulation_type": {"Peraturan"}, "number": {"1"}, "year": {"2026"},
		"issuer": {"Kementerian Contoh"}, "page_title": {"Peraturan Contoh"},
	})
	batch := identityBatch(observation)
	textHash := fixtureHash("e")
	batch.TextArtifacts = []*pb.TextArtifact{{
		Meta:         &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: "text:a"},
		SourceBlobId: "source-blob:a", ParserManifest: fixtureProducerManifest(),
		RawTextRef:        bindingArtifact("raw:a", "raw/a.txt", textHash, 100),
		NormalizedTextRef: bindingArtifact("normalized:a", "normalized/a.txt", textHash, 100),
		MappingRef:        bindingArtifact("mapping:a", "mapping/a.bin", textHash, 100),
		PageResults: []*pb.PageResult{{PageNumber: 1, Status: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED,
			Spans: []*pb.TextSpan{{TextArtifactId: "text:a", EndByte: 100}}}},
		NormalizerManifest: fixtureProducerManifest(),
	}}
	rootID, articleID := "structure:root", "structure:article-1"
	batch.Structures = []*pb.StructureNode{
		{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: rootID},
			Kind: pb.StructureKind_STRUCTURE_KIND_DOCUMENT, Label: "document", OrderedChildren: []string{articleID},
			SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:a", EndByte: 100}}},
		{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: "corpus:fixture", RecordId: articleID},
			Kind: pb.StructureKind_STRUCTURE_KIND_ARTICLE, Label: "Pasal 1", ParentId: &rootID,
			SourceSpans: []*pb.TextSpan{{TextArtifactId: "text:a", StartByte: 10, EndByte: 100}}},
	}
	batch.DependencyManifest.Dependencies = []*pb.Dependency{{DependencyId: "artifact:a", Fingerprint: fixtureHash("a")}}
	plan, err := PlanRegulationIdentities(batch, RegulationIdentityPolicy{Jurisdiction: "ID", MaximumItems: 10})
	if err != nil || len(plan.Candidates) != 1 {
		panic("invalid binding fixture")
	}
	issuerClaim, _ := plan.Candidates[0].CanonicalIssuerClaim()
	regulationClaim, _ := plan.Candidates[0].canonicalRegulationClaim("canonical:organization:a")
	return batch, RegulationDocumentBinding{
		Candidate: plan.Candidates[0], RegulationID: "canonical:regulation:a",
		IssuerID: "canonical:organization:a", IssuerProposalKey: issuerClaim.ProposalKey,
		RegulationProposalKey: regulationClaim.ProposalKey, RegistryRevision: 7,
	}
}

func refreshBindingClaims(binding *RegulationDocumentBinding) {
	issuerClaim, _ := binding.Candidate.CanonicalIssuerClaim()
	regulationClaim, _ := binding.Candidate.canonicalRegulationClaim(binding.IssuerID)
	binding.IssuerProposalKey = issuerClaim.ProposalKey
	binding.RegulationProposalKey = regulationClaim.ProposalKey
}

func setObservationText(observation *pb.SourceObservation, name, value string) {
	for _, item := range observation.PortalMetadata {
		if item.Name == name {
			item.Value = &pb.NamedValue_Text{Text: value}
			return
		}
	}
}

func provisionAssignments(t *testing.T, candidates []ProvisionIdentityCandidate, revision uint64) []CanonicalIdentityAssignment {
	t.Helper()
	result := make([]CanonicalIdentityAssignment, 0, len(candidates))
	for index, candidate := range candidates {
		claim, err := candidate.CanonicalClaim()
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, CanonicalIdentityAssignment{
			ProposalKey: claim.ProposalKey, CanonicalID: "canonical:provision:" + strconv.Itoa(index+1), Revision: revision, Created: true,
		})
	}
	return result
}

func bindingConfig(revision uint64) DocumentBindingConfig {
	return DocumentBindingConfig{
		Software: "regulagraph-server", Build: "test", Language: "id",
		DocumentKind: pb.DocumentKind_DOCUMENT_KIND_REGULATION, RegistryRevision: revision,
		MaximumRecords: 100, WireLimits: WireLimits{MaxBytes: 4 << 20, MaxDepth: 64, MaxItems: 1000},
	}
}

func bindingArtifact(id, key string, hash *pb.ContentHash, size uint64) *pb.ArtifactRef {
	return &pb.ArtifactRef{ArtifactId: id, ContentHash: hash, StorageKey: key, MediaType: "application/octet-stream", ByteSize: size, SchemaVersion: 1}
}
