// Binds structured document artifacts to registry-owned regulation and provision identities.
//
// The planner derives exact provision claims from a canonical regulation ID plus a complete
// structural path. The materializer emits Regulation, DocumentEdition, Provision, and
// ProvisionVersion records while retaining source observations, normalized spans, and dependency
// revisions. It never invents effective dates, treats portal labels as verified issuer identity,
// or allocates canonical IDs. Callers must obtain issuer/regulation/provision assignments from the
// revisioned registry first. Planning is O(N log N) for N structure nodes; benchmark binding
// throughput, peak RSS, false merge/split, and source coverage against configs/benchmark-targets.yaml.
// Required numeric targets remain REQUIRED_UNMEASURED.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const ProvisionIdentityKeyNamespace = "provision-structural-path:v1"
const CanonicalEntityTypeProvision int16 = 3

// RegulationDocumentBinding supplies registry-owned identities for one exact source candidate.
// IssuerID is resolved separately because a sourced issuer label is not itself a stable identity.
type RegulationDocumentBinding struct {
	Candidate             RegulationIdentityCandidate
	RegulationID          string
	IssuerID              string
	IssuerProposalKey     string
	RegulationProposalKey string
	RegistryRevision      uint64
}

// ProvisionIdentityCandidate is an immutable local-to-canonical proposal. Text and source fields
// are retained in the payload hash but excluded from the identity key so editions can share one
// logical provision when their canonical regulation and structural path agree exactly.
type ProvisionIdentityCandidate struct {
	StructureNodeID       string
	ParentStructureNodeID string
	SourceBlobID          string
	TextArtifactID        string
	RegulationID          string
	StructuralPath        []string
	Spans                 []*pb.TextSpan
	IdentityKey           string
}

// DocumentBindingConfig records the authoritative registry revision and deterministic producer
// identity used by the enriched immutable DocumentBatch.
type DocumentBindingConfig struct {
	Software          string
	Build             string
	Language          string
	Jurisdiction      string
	RegistryBatchSize int
	DocumentKind      pb.DocumentKind
	RegistryRevision  uint64
	MaximumRecords    int
	WireLimits        WireLimits
}

// CanonicalClaim binds a provision proposal to both its exact path identity and complete sourced
// payload. Recomputing the identity prevents mutation with a retained stale key.
func (candidate ProvisionIdentityCandidate) CanonicalClaim() (CanonicalIdentityClaim, error) {
	if !validDocumentID(candidate.StructureNodeID) || !validDocumentID(candidate.SourceBlobID) ||
		!validDocumentID(candidate.TextArtifactID) || !validDocumentID(candidate.RegulationID) ||
		len(candidate.StructuralPath) == 0 || len(candidate.Spans) == 0 {
		return CanonicalIdentityClaim{}, errors.New("complete provision identity candidate is required")
	}
	identityKey := provisionIdentityKey(candidate.RegulationID, candidate.StructuralPath)
	if candidate.IdentityKey != identityKey {
		return CanonicalIdentityClaim{}, errors.New("provision identity candidate key is stale or forged")
	}
	payload, err := json.Marshal(candidate)
	if err != nil {
		return CanonicalIdentityClaim{}, fmt.Errorf("marshal provision identity candidate: %w", err)
	}
	payloadHash := sha256.Sum256(payload)
	proposalHash := sha256.Sum256([]byte(strings.Join([]string{
		candidate.StructureNodeID, candidate.SourceBlobID, candidate.IdentityKey,
	}, "\x00")))
	return CanonicalIdentityClaim{
		ProposalKey:   "provision-proposal:" + hex.EncodeToString(proposalHash[:]),
		EntityType:    CanonicalEntityTypeProvision,
		IdentityScope: ProvisionIdentityKeyNamespace,
		IdentityKey:   candidate.IdentityKey,
		PayloadHash:   hex.EncodeToString(payloadHash[:]),
	}, nil
}

// PlanProvisionIdentities converts each source-mapped structure node into an exact provision claim.
// Missing regulation bindings are left for the regulation review path rather than silently assigned.
func PlanProvisionIdentities(
	batch *pb.DocumentBatch,
	bindings []RegulationDocumentBinding,
	maximumItems int,
) ([]ProvisionIdentityCandidate, error) {
	return planProvisionIdentities(batch, bindings, maximumItems, DefaultWireLimits)
}

func planProvisionIdentities(
	batch *pb.DocumentBatch,
	bindings []RegulationDocumentBinding,
	maximumItems int,
	wireLimits WireLimits,
) ([]ProvisionIdentityCandidate, error) {
	if batch == nil || batch.GetMeta() == nil || maximumItems <= 0 {
		return nil, errors.New("document batch and positive provision item limit are required")
	}
	if len(batch.Structures) > maximumItems || len(batch.TextArtifacts) > maximumItems || len(bindings) > maximumItems {
		return nil, errors.New("provision identity input exceeds configured item limit")
	}
	if len(batch.Structures) == 0 || len(batch.TextArtifacts) == 0 {
		return nil, errors.New("provision planning requires structured text records")
	}
	if err := ValidateWire(batch, wireLimits); err != nil {
		return nil, fmt.Errorf("validate provision identity input: %w", err)
	}
	if len(batch.Regulations) != 0 || len(batch.Editions) != 0 || len(batch.Provisions) != 0 ||
		len(batch.Versions) != 0 || len(batch.Chunks) != 0 {
		return nil, errors.New("provision planning requires an unbound STRUCTURE batch")
	}

	sources := make(map[string]bool, len(batch.Sources))
	for _, source := range batch.Sources {
		id := source.GetMeta().GetRecordId()
		if sources[id] {
			return nil, errors.New("duplicate source blob in provision input")
		}
		sources[id] = true
	}
	bindingBySource := make(map[string]RegulationDocumentBinding, len(bindings))
	observationsBySource := make(map[string][]*pb.SourceObservation, len(batch.Sources))
	for _, observation := range batch.Observations {
		if observation.GetSourceBlobId() != "" {
			observationsBySource[observation.GetSourceBlobId()] = append(observationsBySource[observation.GetSourceBlobId()], observation)
		}
	}
	for _, binding := range bindings {
		if err := validateRegulationBinding(binding); err != nil {
			return nil, err
		}
		if !sources[binding.Candidate.SourceBlobID] {
			return nil, errors.New("regulation binding references a source outside the document batch")
		}
		if _, exists := bindingBySource[binding.Candidate.SourceBlobID]; exists {
			return nil, errors.New("duplicate regulation binding for source blob")
		}
		fresh, problems := regulationCandidate(
			binding.Candidate.SourceBlobID,
			observationsBySource[binding.Candidate.SourceBlobID],
			RegulationIdentityPolicy{Jurisdiction: binding.Candidate.Jurisdiction, MaximumItems: maximumItems},
		)
		if len(problems) != 0 || !equalRegulationCandidates(fresh, binding.Candidate) {
			return nil, errors.New("regulation binding candidate differs from current source observations")
		}
		bindingBySource[binding.Candidate.SourceBlobID] = binding
	}

	textSource := make(map[string]string, len(batch.TextArtifacts))
	for _, artifact := range batch.TextArtifacts {
		id := artifact.GetMeta().GetRecordId()
		if _, exists := textSource[id]; exists || !sources[artifact.SourceBlobId] {
			return nil, errors.New("text artifact identity or source reference is invalid")
		}
		textSource[id] = artifact.SourceBlobId
	}
	nodes := make(map[string]*pb.StructureNode, len(batch.Structures))
	for _, node := range batch.Structures {
		id := node.GetMeta().GetRecordId()
		if _, exists := nodes[id]; exists {
			return nil, errors.New("duplicate structure node in provision input")
		}
		nodes[id] = node
	}
	if err := validateStructureBindingClosure(nodes, textSource, batch.TextArtifacts); err != nil {
		return nil, err
	}

	pathCache := make(map[string][]string, len(nodes))
	visiting := make(map[string]bool, len(nodes))
	var structuralPath func(string) ([]string, error)
	structuralPath = func(id string) ([]string, error) {
		if cached, exists := pathCache[id]; exists {
			return append([]string(nil), cached...), nil
		}
		node := nodes[id]
		if node == nil {
			return nil, errors.New("structure node references an unknown parent")
		}
		if visiting[id] {
			return nil, errors.New("structure parent cycle in provision input")
		}
		visiting[id] = true
		path := []string{}
		if node.ParentId != nil {
			parent, err := structuralPath(node.GetParentId())
			if err != nil {
				return nil, err
			}
			if len(parent) >= wireLimits.MaxDepth {
				return nil, errors.New("structure path exceeds configured depth limit")
			}
			path = append(path, parent...)
		}
		component := strconv.Itoa(int(node.Kind)) + ":" + strings.Join(strings.Fields(strings.TrimSpace(node.Label)), " ")
		path = append(path, component)
		pathBytes := 0
		for _, part := range path {
			pathBytes += len(part)
		}
		if pathBytes > wireLimits.MaxBytes {
			return nil, errors.New("structure path exceeds configured byte limit")
		}
		visiting[id] = false
		pathCache[id] = append([]string(nil), path...)
		return path, nil
	}
	nodeIDs := make([]string, 0, len(nodes))
	for id := range nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	for _, id := range nodeIDs {
		if _, err := structuralPath(id); err != nil {
			return nil, err
		}
	}

	candidates := make([]ProvisionIdentityCandidate, 0, len(batch.Structures))
	seenLocal := make(map[string]bool, len(batch.Structures))
	for _, node := range batch.Structures {
		if len(node.SourceSpans) == 0 {
			return nil, errors.New("structure node has no source span")
		}
		textID := node.SourceSpans[0].TextArtifactId
		for _, span := range node.SourceSpans {
			if span.TextArtifactId != textID {
				return nil, errors.New("one structure node spans multiple text artifacts")
			}
		}
		sourceID, exists := textSource[textID]
		if !exists {
			return nil, errors.New("structure node references an unknown text artifact")
		}
		binding, bound := bindingBySource[sourceID]
		if !bound {
			continue
		}
		path, err := structuralPath(node.GetMeta().GetRecordId())
		if err != nil {
			return nil, err
		}
		identityKey := provisionIdentityKey(binding.RegulationID, path)
		localKey := sourceID + "\x00" + identityKey
		if seenLocal[localKey] {
			return nil, errors.New("ambiguous duplicate structural path within one source")
		}
		seenLocal[localKey] = true
		spans := make([]*pb.TextSpan, len(node.SourceSpans))
		for index, span := range node.SourceSpans {
			spans[index] = proto.Clone(span).(*pb.TextSpan)
		}
		candidates = append(candidates, ProvisionIdentityCandidate{
			StructureNodeID: node.GetMeta().GetRecordId(), ParentStructureNodeID: node.GetParentId(),
			SourceBlobID: sourceID, TextArtifactID: textID, RegulationID: binding.RegulationID,
			StructuralPath: path, Spans: spans, IdentityKey: identityKey,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].StructureNodeID == candidates[j].StructureNodeID {
			return candidates[i].SourceBlobID < candidates[j].SourceBlobID
		}
		return candidates[i].StructureNodeID < candidates[j].StructureNodeID
	})
	return candidates, nil
}

// BindDocumentBatch materializes a new immutable batch from registry assignments. Legal intervals
// remain explicitly unknown and all sourced metadata stays unreviewed until a separate decision.
func BindDocumentBatch(
	batch *pb.DocumentBatch,
	bindings []RegulationDocumentBinding,
	candidates []ProvisionIdentityCandidate,
	assignments []CanonicalIdentityAssignment,
	config DocumentBindingConfig,
) (*pb.DocumentBatch, error) {
	if err := validateDocumentBindingConfig(config); err != nil {
		return nil, err
	}
	planned, err := planProvisionIdentities(batch, bindings, config.MaximumRecords, config.WireLimits)
	if err != nil {
		return nil, err
	}
	if !equalProvisionPlans(planned, candidates) {
		return nil, errors.New("provision candidates differ from a fresh plan")
	}

	assignmentByProposal := make(map[string]CanonicalIdentityAssignment, len(assignments))
	for _, assignment := range assignments {
		if !validDocumentID(assignment.ProposalKey) || !validDocumentID(assignment.CanonicalID) ||
			assignment.Revision == 0 || assignment.Revision > config.RegistryRevision {
			return nil, errors.New("invalid provision registry assignment")
		}
		if _, exists := assignmentByProposal[assignment.ProposalKey]; exists {
			return nil, errors.New("duplicate provision registry assignment")
		}
		assignmentByProposal[assignment.ProposalKey] = assignment
	}
	if len(assignmentByProposal) != len(candidates) {
		return nil, errors.New("provision assignment cardinality mismatch")
	}

	regulations := make(map[string]*pb.Regulation)
	editionBySource := make(map[string]string, len(bindings))
	editions := make([]*pb.DocumentEdition, 0, len(bindings))
	for _, binding := range bindings {
		if err := validateRegulationBinding(binding); err != nil || binding.RegistryRevision > config.RegistryRevision {
			if err == nil {
				err = errors.New("regulation binding revision is ahead of materialization revision")
			}
			return nil, err
		}
		current := &pb.Regulation{
			Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: batch.GetMeta().GetCorpusId(), RecordId: binding.RegulationID},
			Kind: binding.Candidate.Kind, IssuerId: binding.IssuerID, Jurisdiction: binding.Candidate.Jurisdiction,
			OfficialNumber: binding.Candidate.OfficialNumber, Year: binding.Candidate.Year,
			Title: binding.Candidate.Title, IdentityStatus: pb.IdentityStatus_IDENTITY_STATUS_UNRESOLVED,
		}
		if previous := regulations[binding.RegulationID]; previous != nil {
			if normalizeIdentityText(previous.Kind) != normalizeIdentityText(current.Kind) || previous.IssuerId != current.IssuerId ||
				normalizeIdentityText(previous.Jurisdiction) != normalizeIdentityText(current.Jurisdiction) ||
				normalizeIdentityText(previous.OfficialNumber) != normalizeIdentityText(current.OfficialNumber) || previous.Year != current.Year {
				return nil, errors.New("one canonical regulation received conflicting exact identity fields")
			}
			previous.Kind = preferredDisplay(previous.Kind, current.Kind)
			previous.Jurisdiction = preferredDisplay(previous.Jurisdiction, current.Jurisdiction)
			previous.OfficialNumber = preferredDisplay(previous.OfficialNumber, current.OfficialNumber)
			if current.Title < previous.Title {
				previous.Title = current.Title
			}
		} else {
			regulations[binding.RegulationID] = current
		}
		editionID := derivedDocumentID("edition", binding.RegulationID, binding.Candidate.SourceBlobID)
		editionBySource[binding.Candidate.SourceBlobID] = editionID
		editions = append(editions, &pb.DocumentEdition{
			Meta:         &pb.RecordMeta{SchemaVersion: 1, CorpusId: batch.GetMeta().GetCorpusId(), RecordId: editionID},
			RegulationId: &binding.RegulationID, SourceRefs: append([]string(nil), binding.Candidate.ObservationIDs...),
			Language: config.Language, DocumentKind: config.DocumentKind,
			MetadataAssertions: regulationMetadataAssertions(binding.Candidate),
		})
	}

	textArtifacts := make(map[string]*pb.TextArtifact, len(batch.TextArtifacts))
	for _, artifact := range batch.TextArtifacts {
		textArtifacts[artifact.GetMeta().GetRecordId()] = artifact
	}
	provisionIDByStructure := make(map[string]string, len(candidates))
	assignmentByStructure := make(map[string]CanonicalIdentityAssignment, len(candidates))
	for _, candidate := range candidates {
		claim, claimErr := candidate.CanonicalClaim()
		if claimErr != nil {
			return nil, claimErr
		}
		assignment, exists := assignmentByProposal[claim.ProposalKey]
		if !exists {
			return nil, errors.New("missing provision registry assignment")
		}
		provisionIDByStructure[candidate.StructureNodeID] = assignment.CanonicalID
		assignmentByStructure[candidate.StructureNodeID] = assignment
	}

	provisionByID := make(map[string]*pb.Provision)
	versions := make([]*pb.ProvisionVersion, 0, len(candidates))
	for _, candidate := range candidates {
		assignment := assignmentByStructure[candidate.StructureNodeID]
		var parentID *string
		if candidate.ParentStructureNodeID != "" {
			resolved, exists := provisionIDByStructure[candidate.ParentStructureNodeID]
			if !exists {
				return nil, errors.New("bound provision parent is missing")
			}
			parentID = &resolved
		}
		provision := &pb.Provision{
			Meta:         &pb.RecordMeta{SchemaVersion: 1, CorpusId: batch.GetMeta().GetCorpusId(), RecordId: assignment.CanonicalID},
			RegulationId: candidate.RegulationID, StructuralPath: append([]string(nil), candidate.StructuralPath...),
			ParentProvisionId: parentID,
		}
		if previous := provisionByID[assignment.CanonicalID]; previous != nil {
			if previous.RegulationId != provision.RegulationId || !equalNormalizedStrings(previous.StructuralPath, provision.StructuralPath) ||
				previous.GetParentProvisionId() != provision.GetParentProvisionId() {
				return nil, errors.New("one canonical provision received conflicting structure")
			}
			for index := range previous.StructuralPath {
				previous.StructuralPath[index] = preferredDisplay(previous.StructuralPath[index], provision.StructuralPath[index])
			}
		} else {
			provisionByID[assignment.CanonicalID] = provision
		}
		artifact := textArtifacts[candidate.TextArtifactID]
		if artifact == nil || artifact.NormalizedTextRef == nil {
			return nil, errors.New("bound provision version is missing normalized text")
		}
		editionID, exists := editionBySource[candidate.SourceBlobID]
		if !exists {
			return nil, errors.New("bound provision version is missing an edition")
		}
		versionID := provisionVersionID(assignment.CanonicalID, editionID, artifact.NormalizedTextRef, candidate.Spans)
		spans := make([]*pb.TextSpan, len(candidate.Spans))
		for index, span := range candidate.Spans {
			spans[index] = proto.Clone(span).(*pb.TextSpan)
		}
		versions = append(versions, &pb.ProvisionVersion{
			Meta:        &pb.RecordMeta{SchemaVersion: 1, CorpusId: batch.GetMeta().GetCorpusId(), RecordId: versionID},
			ProvisionId: assignment.CanonicalID,
			TextRef:     proto.Clone(artifact.NormalizedTextRef).(*pb.ArtifactRef), Spans: spans,
			LegalInterval: &pb.LegalInterval{
				Start: &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN},
				End:   &pb.DateAssertion{Knowledge: pb.DateKnowledge_DATE_KNOWLEDGE_UNKNOWN},
			},
			LegalStatus: pb.LegalStatus_LEGAL_STATUS_UNKNOWN,
			ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED,
		})
	}

	regulationRecords := make([]*pb.Regulation, 0, len(regulations))
	for _, regulation := range regulations {
		regulationRecords = append(regulationRecords, regulation)
	}
	provisionRecords := make([]*pb.Provision, 0, len(provisionByID))
	for _, provision := range provisionByID {
		provisionRecords = append(provisionRecords, provision)
	}
	sort.Slice(regulationRecords, func(i, j int) bool {
		return regulationRecords[i].GetMeta().GetRecordId() < regulationRecords[j].GetMeta().GetRecordId()
	})
	sort.Slice(editions, func(i, j int) bool { return editions[i].GetMeta().GetRecordId() < editions[j].GetMeta().GetRecordId() })
	sort.Slice(provisionRecords, func(i, j int) bool {
		return provisionRecords[i].GetMeta().GetRecordId() < provisionRecords[j].GetMeta().GetRecordId()
	})
	sort.Slice(versions, func(i, j int) bool { return versions[i].GetMeta().GetRecordId() < versions[j].GetMeta().GetRecordId() })

	inputRaw, err := proto.MarshalOptions{Deterministic: true}.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("marshal binding input: %w", err)
	}
	inputHash := sha256.Sum256(inputRaw)
	identityParts := []string{batch.GetMeta().GetRecordId(), strconv.FormatUint(config.RegistryRevision, 10)}
	for _, regulation := range regulationRecords {
		identityParts = append(identityParts, regulation.GetMeta().GetRecordId())
	}
	for _, provision := range provisionRecords {
		identityParts = append(identityParts, provision.GetMeta().GetRecordId())
	}
	producer := bindingProducerManifest(config, inputHash)
	identityParts = append(identityParts,
		hex.EncodeToString(inputHash[:]), producer.Software, producer.Build, producer.GetConfigHash().GetSha256())
	outputIdentity := derivedDocumentID("bound", identityParts...)
	dependencies, err := bindingDependencies(batch, inputHash, bindings, config.RegistryRevision)
	if err != nil {
		return nil, err
	}
	lookupRevisions := bindingLookupRevisions(batch, config.RegistryRevision)

	output := proto.Clone(batch).(*pb.DocumentBatch)
	output.Meta.RecordId = "document-batch:" + strings.TrimPrefix(outputIdentity, "bound:")
	output.Regulations = regulationRecords
	output.Editions = editions
	output.Provisions = provisionRecords
	output.Versions = versions
	output.Chunks = nil
	output.DependencyManifest = &pb.DependencyManifest{
		ArtifactId:   "dependency-manifest:" + strings.TrimPrefix(outputIdentity, "bound:"),
		Dependencies: dependencies, ProducerManifest: producer, LookupScopeRevisions: lookupRevisions,
	}
	if len(bindings) < len(batch.Sources) {
		degradeToPartial(output)
		appendUnboundSourceIssues(output, bindings)
	}
	if missingStructuredSources := boundSourcesWithoutText(batch, bindings); len(missingStructuredSources) != 0 {
		degradeToPartial(output)
		appendMissingStructuredSourceIssues(output, missingStructuredSources)
	}
	if documentRecordCount(output) > config.MaximumRecords {
		return nil, errors.New("bound document batch exceeds configured record limit")
	}
	if err = ValidateWire(output, config.WireLimits); err != nil {
		return nil, fmt.Errorf("validate bound document batch: %w", err)
	}
	return output, nil
}

func validateStructureBindingClosure(
	nodes map[string]*pb.StructureNode,
	textSource map[string]string,
	textArtifacts []*pb.TextArtifact,
) error {
	textSize := make(map[string]uint64, len(textArtifacts))
	for _, artifact := range textArtifacts {
		if artifact.NormalizedTextRef == nil {
			return errors.New("structure binding requires normalized text references")
		}
		textSize[artifact.GetMeta().GetRecordId()] = artifact.NormalizedTextRef.ByteSize
	}
	documentRoot := make(map[string]string, len(textArtifacts))
	childOwner := make(map[string]string, len(nodes))
	for id, node := range nodes {
		if len(node.SourceSpans) == 0 {
			return errors.New("structure node has no source span")
		}
		textID := node.SourceSpans[0].TextArtifactId
		if _, exists := textSource[textID]; !exists {
			return errors.New("structure node references an unknown text artifact")
		}
		var previousSpanEnd uint64
		for index, span := range node.SourceSpans {
			if span.TextArtifactId != textID || span.EndByte > textSize[textID] || span.EndByte <= span.StartByte ||
				(index > 0 && span.StartByte < previousSpanEnd) {
				return errors.New("structure node has an invalid or overlapping normalized source span")
			}
			previousSpanEnd = span.EndByte
		}
		if node.ParentId == nil {
			if node.Kind != pb.StructureKind_STRUCTURE_KIND_DOCUMENT {
				return errors.New("structure root must be a document node")
			}
			if _, exists := documentRoot[textID]; exists {
				return errors.New("text artifact has multiple document roots")
			}
			documentRoot[textID] = id
		}
		seenChildren := make(map[string]bool, len(node.OrderedChildren))
		var previousEnd uint64
		for _, childID := range node.OrderedChildren {
			child := nodes[childID]
			if seenChildren[childID] || child == nil || child.GetParentId() != id || len(child.SourceSpans) == 0 ||
				child.SourceSpans[0].StartByte < previousEnd {
				return errors.New("structure ordered child closure is invalid")
			}
			if owner, exists := childOwner[childID]; exists && owner != id {
				return errors.New("structure child belongs to multiple parents")
			}
			seenChildren[childID] = true
			childOwner[childID] = id
			previousEnd = child.SourceSpans[len(child.SourceSpans)-1].EndByte
		}
	}
	for textID, size := range textSize {
		root := nodes[documentRoot[textID]]
		if root == nil || !spansCoverRange(root.SourceSpans, size) {
			return errors.New("text artifact lacks one full-coverage document root")
		}
	}
	for id, node := range nodes {
		textID := node.SourceSpans[0].TextArtifactId
		if node.ParentId != nil {
			parent := nodes[node.GetParentId()]
			if parent == nil || len(parent.SourceSpans) == 0 || parent.SourceSpans[0].TextArtifactId != textID ||
				childOwner[id] != node.GetParentId() || !spansContain(parent.SourceSpans, node.SourceSpans) {
				return errors.New("structure parent/child closure is invalid")
			}
		}
	}
	return nil
}

func spansContain(parents, children []*pb.TextSpan) bool {
	parentIndex := 0
	for _, child := range children {
		for parentIndex < len(parents) && parents[parentIndex].EndByte <= child.StartByte {
			parentIndex++
		}
		if parentIndex == len(parents) || parents[parentIndex].TextArtifactId != child.TextArtifactId ||
			parents[parentIndex].StartByte > child.StartByte || parents[parentIndex].EndByte < child.EndByte {
			return false
		}
	}
	return true
}

func spansCoverRange(spans []*pb.TextSpan, size uint64) bool {
	if len(spans) == 0 || spans[0].StartByte != 0 || spans[len(spans)-1].EndByte != size {
		return false
	}
	for index := 1; index < len(spans); index++ {
		if spans[index-1].EndByte != spans[index].StartByte {
			return false
		}
	}
	return true
}

func appendUnboundSourceIssues(batch *pb.DocumentBatch, bindings []RegulationDocumentBinding) {
	bound := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		bound[binding.Candidate.SourceBlobID] = true
	}
	observations := map[string][]string{}
	for _, observation := range batch.Observations {
		if observation.GetSourceBlobId() != "" {
			observations[observation.GetSourceBlobId()] = append(observations[observation.GetSourceBlobId()], observation.GetMeta().GetRecordId())
		}
	}
	for _, source := range batch.Sources {
		sourceID := source.GetMeta().GetRecordId()
		if bound[sourceID] {
			continue
		}
		refs := append([]string(nil), observations[sourceID]...)
		if len(refs) == 0 {
			refs = []string{sourceID}
		}
		sort.Strings(refs)
		batch.Issues = append(batch.Issues, &pb.ValidationIssue{
			Code: "REGULATION_IDENTITY_REVIEW_REQUIRED", Severity: pb.Severity_SEVERITY_ERROR,
			RecordId: sourceID, FieldPath: "regulations", EvidenceRefs: refs,
			Disposition: "quarantine until a registry-owned regulation and issuer identity are assigned",
		})
	}
	sort.Slice(batch.Issues, func(i, j int) bool {
		if batch.Issues[i].RecordId == batch.Issues[j].RecordId {
			return batch.Issues[i].Code < batch.Issues[j].Code
		}
		return batch.Issues[i].RecordId < batch.Issues[j].RecordId
	})
}

func boundSourcesWithoutText(batch *pb.DocumentBatch, bindings []RegulationDocumentBinding) map[string][]string {
	withText := make(map[string]bool, len(batch.TextArtifacts))
	for _, artifact := range batch.TextArtifacts {
		withText[artifact.SourceBlobId] = true
	}
	missing := make(map[string][]string)
	for _, binding := range bindings {
		if !withText[binding.Candidate.SourceBlobID] {
			missing[binding.Candidate.SourceBlobID] = append([]string(nil), binding.Candidate.ObservationIDs...)
		}
	}
	return missing
}

func appendMissingStructuredSourceIssues(batch *pb.DocumentBatch, missing map[string][]string) {
	for sourceID, observationIDs := range missing {
		refs := append([]string(nil), observationIDs...)
		sort.Strings(refs)
		batch.Issues = append(batch.Issues, &pb.ValidationIssue{
			Code: "STRUCTURED_TEXT_MISSING", Severity: pb.Severity_SEVERITY_ERROR,
			RecordId: sourceID, FieldPath: "text_artifacts", EvidenceRefs: refs,
			Disposition: "retain edition provenance but quarantine provision publication until structured text is available",
		})
	}
	sort.Slice(batch.Issues, func(i, j int) bool {
		if batch.Issues[i].RecordId == batch.Issues[j].RecordId {
			return batch.Issues[i].Code < batch.Issues[j].Code
		}
		return batch.Issues[i].RecordId < batch.Issues[j].RecordId
	})
}

func degradeToPartial(batch *pb.DocumentBatch) {
	if batch.Completeness == pb.Completeness_COMPLETENESS_COMPLETE {
		batch.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	}
}

func validateRegulationBinding(binding RegulationDocumentBinding) error {
	if !validDocumentID(binding.RegulationID) || !validDocumentID(binding.IssuerID) ||
		!validDocumentID(binding.IssuerProposalKey) || !validDocumentID(binding.RegulationProposalKey) ||
		binding.RegistryRevision == 0 {
		return errors.New("complete registry-owned regulation binding is required")
	}
	issuerClaim, err := binding.Candidate.CanonicalIssuerClaim()
	if err != nil {
		return fmt.Errorf("invalid regulation binding candidate: %w", err)
	}
	regulationClaim, err := binding.Candidate.canonicalRegulationClaim(binding.IssuerID)
	if err != nil || issuerClaim.ProposalKey != binding.IssuerProposalKey || regulationClaim.ProposalKey != binding.RegulationProposalKey {
		return errors.New("regulation binding differs from its registry proposals")
	}
	return nil
}

func equalRegulationCandidates(left, right RegulationIdentityCandidate) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func validateDocumentBindingConfig(config DocumentBindingConfig) error {
	if strings.TrimSpace(config.Software) == "" || strings.TrimSpace(config.Build) == "" ||
		strings.TrimSpace(config.Language) == "" || strings.TrimSpace(config.Jurisdiction) == "" || config.RegistryBatchSize <= 0 ||
		config.DocumentKind == pb.DocumentKind_DOCUMENT_KIND_UNSPECIFIED ||
		config.MaximumRecords <= 0 || config.WireLimits.MaxBytes <= 0 ||
		config.WireLimits.MaxDepth <= 0 || config.WireLimits.MaxItems <= 0 {
		return errors.New("complete bounded document binding config is required")
	}
	return nil
}

func provisionIdentityKey(regulationID string, path []string) string {
	payload := struct {
		Namespace    string   `json:"namespace"`
		RegulationID string   `json:"regulation_id"`
		Path         []string `json:"path"`
	}{Namespace: ProvisionIdentityKeyNamespace, RegulationID: regulationID, Path: make([]string, len(path))}
	for index, component := range path {
		payload.Path[index] = normalizeIdentityText(component)
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func regulationMetadataAssertions(candidate RegulationIdentityCandidate) []*pb.MetadataAssertion {
	refs := append([]string(nil), candidate.ObservationIDs...)
	fields := []struct{ name, value string }{
		{"kind", candidate.Kind}, {"issuer_label", candidate.IssuerLabel}, {"jurisdiction", candidate.Jurisdiction},
		{"official_number", candidate.OfficialNumber}, {"year", strconv.FormatUint(uint64(candidate.Year), 10)}, {"title", candidate.Title},
	}
	result := make([]*pb.MetadataAssertion, 0, len(fields))
	for _, field := range fields {
		result = append(result, &pb.MetadataAssertion{Field: field.name, Value: field.value,
			ObservationRefs: append([]string(nil), refs...), ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED})
	}
	return result
}

func provisionVersionID(provisionID, editionID string, textRef *pb.ArtifactRef, spans []*pb.TextSpan) string {
	parts := []string{provisionID, editionID, textRef.GetContentHash().GetSha256()}
	for _, span := range spans {
		parts = append(parts, span.TextArtifactId, strconv.FormatUint(span.StartByte, 10), strconv.FormatUint(span.EndByte, 10))
	}
	return derivedDocumentID("provision-version", parts...)
}

func derivedDocumentID(prefix string, parts ...string) string {
	hasher := sha256.New()
	for _, part := range append([]string{prefix + "-v1"}, parts...) {
		_, _ = hasher.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(part))
	}
	return prefix + ":" + hex.EncodeToString(hasher.Sum(nil))
}

func bindingProducerManifest(config DocumentBindingConfig, inputHash [sha256.Size]byte) *pb.ProducerManifest {
	configRaw, _ := json.Marshal(struct {
		Language          string `json:"language"`
		Jurisdiction      string `json:"jurisdiction"`
		Kind              int32  `json:"document_kind"`
		RegistryBatchSize int    `json:"registry_batch_size"`
	}{config.Language, config.Jurisdiction, int32(config.DocumentKind), config.RegistryBatchSize})
	configHash := sha256.Sum256(configRaw)
	return &pb.ProducerManifest{
		Software: config.Software, Build: config.Build, SchemaVersion: 1,
		ConfigHash:  &pb.ContentHash{Sha256: hex.EncodeToString(configHash[:])},
		InputHashes: []*pb.ContentHash{{Sha256: hex.EncodeToString(inputHash[:])}},
	}
}

func bindingDependencies(
	batch *pb.DocumentBatch,
	inputHash [sha256.Size]byte,
	bindings []RegulationDocumentBinding,
	registryRevision uint64,
) ([]*pb.Dependency, error) {
	dependencies := make([]*pb.Dependency, 0, len(batch.GetDependencyManifest().GetDependencies())+len(bindings)+1)
	seen := map[string]string{}
	for _, dependency := range batch.GetDependencyManifest().GetDependencies() {
		if dependency.GetFingerprint() == nil {
			return nil, errors.New("binding input dependency is missing a fingerprint")
		}
		id, hash := dependency.DependencyId, dependency.GetFingerprint().GetSha256()
		if previous, exists := seen[id]; exists && previous != hash {
			return nil, errors.New("binding input has conflicting dependency fingerprints")
		} else if exists {
			continue
		}
		seen[id] = hash
		dependencies = append(dependencies, proto.Clone(dependency).(*pb.Dependency))
	}
	inputID, inputDigest := batch.GetMeta().GetRecordId(), hex.EncodeToString(inputHash[:])
	if previous, exists := seen[inputID]; exists && previous != inputDigest {
		return nil, errors.New("binding input batch dependency conflicts with an existing dependency")
	} else if !exists {
		dependencies = append(dependencies, &pb.Dependency{DependencyId: inputID, Fingerprint: &pb.ContentHash{Sha256: inputDigest}})
		seen[inputID] = inputDigest
	}
	for _, binding := range bindings {
		fingerprintRaw := sha256.Sum256([]byte(strings.Join([]string{
			"canonical-registry", "organization", binding.IssuerID, strconv.FormatUint(registryRevision, 10),
		}, "\x00")))
		fingerprint := hex.EncodeToString(fingerprintRaw[:])
		if previous, exists := seen[binding.IssuerID]; exists && previous != fingerprint {
			return nil, errors.New("canonical issuer dependency conflicts with an existing dependency")
		} else if !exists {
			dependencies = append(dependencies, &pb.Dependency{
				DependencyId: binding.IssuerID, Fingerprint: &pb.ContentHash{Sha256: fingerprint},
			})
			seen[binding.IssuerID] = fingerprint
		}
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].DependencyId < dependencies[j].DependencyId })
	return dependencies, nil
}

func bindingLookupRevisions(batch *pb.DocumentBatch, revision uint64) []*pb.LookupScopeRevision {
	byScope := map[string]*pb.LookupScopeRevision{}
	for _, item := range batch.GetDependencyManifest().GetLookupScopeRevisions() {
		byScope[item.ScopeId] = proto.Clone(item).(*pb.LookupScopeRevision)
	}
	byScope["canonical-registry"] = &pb.LookupScopeRevision{
		ScopeId: "canonical-registry", Revision: revision, EmptyResult: revision == 0,
	}
	result := make([]*pb.LookupScopeRevision, 0, len(byScope))
	for _, item := range byScope {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ScopeId < result[j].ScopeId })
	return result
}

func documentRecordCount(batch *pb.DocumentBatch) int {
	return 1 + len(batch.Sources) + len(batch.TextArtifacts) + len(batch.Structures) + len(batch.Provisions) +
		len(batch.Versions) + len(batch.Chunks) + len(batch.Issues) + len(batch.Editions) +
		len(batch.Regulations) + len(batch.Observations) + len(batch.Changes)
}

func equalProvisionPlans(left, right []ProvisionIdentityCandidate) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		leftRaw, _ := json.Marshal(left[index])
		rightRaw, _ := json.Marshal(right[index])
		if string(leftRaw) != string(rightRaw) {
			return false
		}
	}
	return true
}

func equalNormalizedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if normalizeIdentityText(left[index]) != normalizeIdentityText(right[index]) {
			return false
		}
	}
	return true
}

func preferredDisplay(left, right string) string {
	if right < left {
		return right
	}
	return left
}

func validDocumentID(value string) bool {
	return value != "" && len(value) <= 256 && strings.IndexFunc(value, func(character rune) bool {
		return character < 33 || character > 126
	}) < 0
}
