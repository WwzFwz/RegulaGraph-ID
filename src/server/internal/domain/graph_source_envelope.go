// Derives snapshot-bound EXTRACT/RESOLVE envelopes from immutable ingestion artifacts.
// Only outer identity/context, immediate input refs, diagnostic root refs and dependency edges change; model
// outputs, legal records, decisions and their recorded revisions remain byte-semantically
// identical. The indexed DocumentBatch must exactly reproduce the existing snapshot binder.
// This pure transform proves no storage membership, registry freshness or publication
// authority: the coordinator must persist an authenticated original-to-bound receipt.
// Input/output aggregate bytes are capped before decoding/cloning; measure RSS and p95
// under configs/benchmark-targets.yaml (required acceptance remains unmeasured).
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const GraphSourceEnvelopePolicy = "graph-source-snapshot-envelope-v1"

// GraphSourceArtifact is a local Go carrier for existing C01 refs and their bytes,
// not a new wire schema. Storage registration must be authenticated separately.
type GraphSourceArtifact struct {
	Reference *pb.ArtifactRef
	Bytes     []byte
}

// BindGraphSourceEnvelopes accepts the original unpublished CHUNK and its exact
// initial-snapshot envelope, followed by the original EXTRACT and RESOLVE artifacts.
// It returns new content-addressed artifacts without writing storage or calling models.
func BindGraphSourceEnvelopes(sourceJob string, originalDocument, snapshotDocument,
	extraction, resolution GraphSourceArtifact, maximumEdges int) (GraphSourceArtifact, GraphSourceArtifact, error) {
	fail := func(err error) (GraphSourceArtifact, GraphSourceArtifact, error) {
		return GraphSourceArtifact{}, GraphSourceArtifact{}, err
	}
	if maximumEdges <= 0 || maximumEdges > DefaultWireLimits.MaxItems {
		return fail(errors.New("bounded graph source reference budget required"))
	}
	document, boundDocument := new(pb.DocumentBatch), new(pb.DocumentBatch)
	extract, resolve := new(pb.ExtractionBatch), new(pb.ResolutionBatch)
	inputs := []GraphSourceArtifact{originalDocument, snapshotDocument, extraction, resolution}
	messages := []proto.Message{document, boundDocument, extract, resolve}
	remaining := DefaultWireLimits.MaxBytes
	seen := map[string]bool{}
	// Admit all raw lengths before decoding any payload.
	for _, input := range inputs {
		ref := input.Reference
		if err := ValidateWire(ref, DefaultWireLimits); err != nil {
			return fail(err)
		}
		if !graphAssemblyKnownFields(ref.ProtoReflect()) {
			return fail(errors.New("unknown graph source reference fields"))
		}
		if ref.SchemaVersion != 1 || ref.ByteSize == 0 || ref.ByteSize != uint64(len(input.Bytes)) ||
			len(input.Bytes) > remaining || seen[ref.ArtifactId] {
			return fail(errors.New("graph source artifact size, schema or role collision"))
		}
		remaining -= len(input.Bytes)
		seen[ref.ArtifactId] = true
		if fmt.Sprintf("%x", sha256.Sum256(input.Bytes)) != ref.ContentHash.Sha256 {
			return fail(errors.New("graph source bytes differ from reference hash"))
		}
	}
	if !IsDocumentBatchMediaType(originalDocument.Reference.MediaType) ||
		snapshotDocument.Reference.MediaType != originalDocument.Reference.MediaType ||
		(extraction.Reference.MediaType != ExtractionBatchMediaType && extraction.Reference.MediaType != "application/x-protobuf") ||
		(resolution.Reference.MediaType != "application/x-protobuf" && resolution.Reference.MediaType != "application/x-protobuf; message=regulagraph.v1.ResolutionBatch") {
		return fail(errors.New("graph source role media mismatch"))
	}
	for i, message := range messages {
		if err := DecodeWire(inputs[i].Bytes, message, DefaultWireLimits); err != nil {
			return fail(err)
		}
		if !graphAssemblyKnownFields(message.ProtoReflect()) {
			return fail(errors.New("unknown graph source envelope fields"))
		}
	}
	if document.Meta.SchemaVersion != 1 || boundDocument.Meta.SchemaVersion != 1 || extract.Meta.SchemaVersion != 1 || resolve.Meta.SchemaVersion != 1 ||
		document.Meta.Visibility != nil || boundDocument.Meta.Visibility != nil || extract.Meta.Visibility != nil || resolve.Meta.Visibility != nil ||
		extract.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || resolve.Completeness != pb.Completeness_COMPLETENESS_COMPLETE ||
		!proto.Equal(extract.SourceDocumentBatch, originalDocument.Reference) {
		return fail(errors.New("graph source envelopes require complete original source chain"))
	}
	expected, err := BindInitialSnapshotSource(sourceJob, document, originalDocument.Reference,
		boundDocument.Context.SnapshotRef, boundDocument.Context.AuthScopeRef)
	if err != nil {
		return fail(err)
	}
	if !proto.Equal(expected, boundDocument) {
		return fail(errors.New("indexed document differs from exact original snapshot envelope"))
	}
	if err = ValidateExtractionBatchClosure(extract, document, maximumEdges); err != nil {
		return fail(err)
	}
	if err = ValidateResolutionBatchClosure(resolve, extract, extraction.Reference, maximumEdges); err != nil {
		return fail(err)
	}
	// Root references in diagnostics must never be ambiguous with a preserved
	// child record ID. The extraction closure does not reserve the batch root.
	for _, group := range [][]string{graphExtractionChildIDs(extract), graphResolutionChildIDs(resolve)} {
		for _, id := range group {
			if id == extract.Meta.RecordId || id == resolve.Meta.RecordId {
				return fail(errors.New("graph source root collides with child record"))
			}
		}
	}
	// The binder only changes request metadata. Inference producers remain the
	// actual original producers; the policy dependency identifies this transform.
	originalExtractID, originalResolveID := extract.Meta.RecordId, resolve.Meta.RecordId
	extract.Meta.RecordId = graphSourceEnvelopeID("extract", sourceJob, extraction.Reference, snapshotDocument.Reference)
	extract.Context = graphSourceEnvelopeContext(extract.Context, boundDocument.Context, extract.Meta.RecordId)
	extract.SourceDocumentBatch = proto.Clone(snapshotDocument.Reference).(*pb.ArtifactRef)
	if err = bindGraphSourceDependencies(extract.Dependencies, extract.Meta.RecordId, extraction.Reference, snapshotDocument.Reference); err != nil {
		return fail(err)
	}
	if err = ValidateExtractionBatchClosure(extract, boundDocument, maximumEdges); err != nil {
		return fail(err)
	}
	boundExtract, err := encodeGraphSourceEnvelope(extract, extraction.Reference.MediaType, "extraction-batch")
	if err != nil {
		return fail(err)
	}
	resolve.Meta.RecordId = graphSourceEnvelopeID("resolve", sourceJob, resolution.Reference, boundExtract.Reference)
	// Diagnostic references to outer batch IDs follow the new envelope. The
	// original diagnostic bytes remain available through the original artifact
	// dependency. Evidence record IDs and decision/proposal payloads never change.
	for _, issue := range resolve.Issues {
		for i, id := range issue.EvidenceRefs {
			if id == originalExtractID {
				issue.EvidenceRefs[i] = extract.Meta.RecordId
			}
			if id == originalResolveID {
				issue.EvidenceRefs[i] = resolve.Meta.RecordId
			}
		}
	}
	resolve.Context = graphSourceEnvelopeContext(resolve.Context, boundDocument.Context, resolve.Meta.RecordId)
	resolve.SourceExtractionBatch = proto.Clone(boundExtract.Reference).(*pb.ArtifactRef)
	if err = bindGraphSourceDependencies(resolve.Dependencies, resolve.Meta.RecordId, resolution.Reference, boundExtract.Reference); err != nil {
		return fail(err)
	}
	if err = ValidateResolutionBatchClosure(resolve, extract, boundExtract.Reference, maximumEdges); err != nil {
		return fail(err)
	}
	boundResolve, err := encodeGraphSourceEnvelope(resolve, resolution.Reference.MediaType, "resolution-batch")
	if err != nil {
		return fail(err)
	}
	if len(boundExtract.Bytes) > DefaultWireLimits.MaxBytes-len(boundResolve.Bytes) {
		return fail(errors.New("graph source envelopes exceed aggregate output budget"))
	}
	return boundExtract, boundResolve, nil
}

func graphExtractionChildIDs(batch *pb.ExtractionBatch) []string {
	ids := make([]string, 0, len(batch.Mentions)+len(batch.Assertions)+len(batch.Supports))
	for _, item := range batch.Mentions {
		ids = append(ids, item.Meta.RecordId)
	}
	for _, item := range batch.Assertions {
		ids = append(ids, item.Meta.RecordId)
	}
	for _, item := range batch.Supports {
		ids = append(ids, item.Meta.RecordId)
	}
	return ids
}

func graphResolutionChildIDs(batch *pb.ResolutionBatch) []string {
	ids := make([]string, 0, len(batch.Proposals)+len(batch.Decisions))
	for _, item := range batch.Proposals {
		ids = append(ids, item.Meta.RecordId)
	}
	for _, item := range batch.Decisions {
		ids = append(ids, item.Meta.RecordId)
	}
	return ids
}

func graphSourceEnvelopeID(role, sourceJob string, original, parent *pb.ArtifactRef) string {
	h := sha256.New()
	for _, part := range []string{GraphSourceEnvelopePolicy, role, sourceJob, original.ArtifactId,
		original.ContentHash.Sha256, parent.ArtifactId, parent.ContentHash.Sha256} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	return fmt.Sprintf("graph-source:%s:%x", role, h.Sum(nil))
}

func graphSourceEnvelopeContext(original, bound *pb.RequestContext, id string) *pb.RequestContext {
	result := proto.Clone(original).(*pb.RequestContext)
	result.RequestId, result.TraceId = id, id
	result.SnapshotRef = proto.Clone(bound.SnapshotRef).(*pb.SnapshotRef)
	result.ConfigFingerprint = proto.Clone(bound.ConfigFingerprint).(*pb.ContentHash)
	return result
}

func bindGraphSourceDependencies(manifest *pb.DependencyManifest, id string, original, parent *pb.ArtifactRef) error {
	manifest.ArtifactId = id
	policy := &pb.Dependency{DependencyId: "policy:" + GraphSourceEnvelopePolicy,
		Fingerprint: &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256([]byte(GraphSourceEnvelopePolicy)))}}
	additions := []*pb.Dependency{policy}
	for _, ref := range []*pb.ArtifactRef{original, parent} {
		additions = append(additions, &pb.Dependency{DependencyId: ref.ArtifactId,
			Fingerprint: proto.Clone(ref.ContentHash).(*pb.ContentHash)})
	}
	seen := map[string]bool{}
	for _, dependency := range manifest.Dependencies {
		seen[dependency.DependencyId] = true
	}
	for _, dependency := range additions {
		if seen[dependency.DependencyId] {
			return errors.New("graph source binding dependency collision")
		}
		seen[dependency.DependencyId] = true
		manifest.Dependencies = append(manifest.Dependencies, dependency)
	}
	sort.Slice(manifest.Dependencies, func(i, j int) bool {
		return manifest.Dependencies[i].DependencyId < manifest.Dependencies[j].DependencyId
	})
	return nil
}

func encodeGraphSourceEnvelope(message proto.Message, media, kind string) (GraphSourceArtifact, error) {
	if err := ValidateWire(message, DefaultWireLimits); err != nil {
		return GraphSourceArtifact{}, err
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return GraphSourceArtifact{}, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	return GraphSourceArtifact{Reference: &pb.ArtifactRef{ArtifactId: "artifact:" + kind + ":" + hash,
		ContentHash: &pb.ContentHash{Sha256: hash}, StorageKey: "sha256/" + hash[:2] + "/" + hash[2:4] + "/" + hash + ".bin",
		SchemaVersion: 1, MediaType: media, ByteSize: uint64(len(raw))}, Bytes: raw}, nil
}
