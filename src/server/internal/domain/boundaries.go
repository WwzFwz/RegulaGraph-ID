// Cross-record C01 guards bind worker/model/publication responses to their initiating context.
// Pure checks return errors before side effects; actual leases, permissions and database state come from S01.
// Use once per bounded batch, not once per graph edge. Required performance gates remain unmeasured.
package domain

import (
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func VerifyWorkerResponse(req *pb.ProcessBatchRequest, res *pb.ProcessBatchResponse) error {
	if e := ValidateWire(req, DefaultWireLimits); e != nil {
		return e
	}
	if e := ValidateWire(res, DefaultWireLimits); e != nil {
		return e
	}
	if req.Context.RequestId != res.RequestId || req.JobId != res.JobId || req.Attempt != res.Attempt || req.Lease.Fence != res.Fence {
		return errors.New("stale or mismatched worker response")
	}
	if res.Checkpoint != nil && (res.Checkpoint.JobId != req.JobId || res.Checkpoint.Fence != req.Lease.Fence || res.Checkpoint.Meta.CorpusId != req.Context.CorpusId) {
		return errors.New("checkpoint context/fence mismatch")
	}
	if res.Checkpoint != nil {
		if res.Checkpoint.TerminalStatus == pb.CompletionStatus_COMPLETION_STATUS_UNSPECIFIED ||
			res.Checkpoint.TerminalStatus != res.Status {
			return errors.New("checkpoint terminal status does not match worker response")
		}
		if !containsStage(req.Stages, res.Checkpoint.Stage) {
			return errors.New("checkpoint stage was not requested")
		}
		outputs := responseArtifacts(res)
		if len(res.Checkpoint.CompletedBatchKeys) != len(outputs) || len(res.Checkpoint.ArtifactHashes) != len(outputs) {
			return errors.New("checkpoint output cardinality mismatch")
		}
		for index, output := range outputs {
			if output == nil || output.ContentHash == nil || res.Checkpoint.CompletedBatchKeys[index] != output.ArtifactId || !proto.Equal(res.Checkpoint.ArtifactHashes[index], output.ContentHash) {
				return fmt.Errorf("checkpoint output binding mismatch at index %d", index)
			}
		}
	}
	if res.DocumentBatch != nil && !containsStage(req.Stages, pb.JobStage_JOB_STAGE_PARSE) &&
		!containsStage(req.Stages, pb.JobStage_JOB_STAGE_STRUCTURE) && !containsStage(req.Stages, pb.JobStage_JOB_STAGE_CHUNK) {
		return errors.New("document batch returned without requested PARSE, STRUCTURE, or CHUNK stage")
	}
	if res.GraphDelta != nil && !containsStage(req.Stages, pb.JobStage_JOB_STAGE_ASSEMBLE) {
		return errors.New("graph delta returned without requested ASSEMBLE stage")
	}
	if res.IndexBatch != nil && !containsStage(req.Stages, pb.JobStage_JOB_STAGE_INDEX) {
		return errors.New("index batch returned without requested INDEX stage")
	}
	if res.Status == pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED && (len(res.Errors) > 0 || res.DocumentBatch == nil && res.GraphDelta == nil && res.IndexBatch == nil) {
		return errors.New("successful worker response requires output and no errors")
	}
	return nil
}

func containsStage(stages []pb.JobStage, expected pb.JobStage) bool {
	for _, stage := range stages {
		if stage == expected {
			return true
		}
	}
	return false
}

func responseArtifacts(response *pb.ProcessBatchResponse) []*pb.ArtifactRef {
	outputs := make([]*pb.ArtifactRef, 0, 3)
	if response.DocumentBatch != nil {
		outputs = append(outputs, response.DocumentBatch)
	}
	if response.GraphDelta != nil {
		outputs = append(outputs, response.GraphDelta)
	}
	if response.IndexBatch != nil {
		outputs = append(outputs, response.IndexBatch)
	}
	return outputs
}

func VerifyRerankResults(req *pb.RerankBatchRequest, res *pb.RerankBatchResponse) error {
	if e := ValidateWire(req, DefaultWireLimits); e != nil {
		return e
	}
	if e := ValidateWire(res, DefaultWireLimits); e != nil {
		return e
	}
	if req.Context.RequestId != res.RequestId || !proto.Equal(req.Model, res.Model) {
		return errors.New("rerank response context/model mismatch")
	}
	expected := map[string]bool{}
	for _, p := range req.Pairs {
		expected[p.PairId] = true
	}
	for _, p := range res.Results {
		if !expected[p.PairId] {
			return errors.New("unexpected/duplicate rerank result")
		}
		delete(expected, p.PairId)
	}
	if len(expected) > 0 {
		return errors.New("missing rerank results")
	}
	return nil
}

func VerifyPublicationReady(manifest *pb.PublicationManifest, parent *pb.SnapshotRef, fence uint64) error {
	if e := ValidateWire(manifest, DefaultWireLimits); e != nil {
		return e
	}
	if manifest.Fence != fence || !proto.Equal(manifest.ParentRef, parent) {
		return errors.New("publication parent/fence mismatch")
	}
	if parent != nil && (parent.CorpusId != manifest.SnapshotRef.CorpusId || parent.Sequence >= manifest.SnapshotRef.Sequence) {
		return errors.New("publication snapshot sequence/corpus mismatch")
	}
	if manifest.Meta.CorpusId != manifest.SnapshotRef.CorpusId || !manifest.ValidationReport.Valid {
		return errors.New("invalid publication report/corpus")
	}
	for _, issue := range manifest.ValidationReport.Issues {
		if issue.Severity == pb.Severity_SEVERITY_ERROR {
			return errors.New("publication report has invariant errors")
		}
	}
	expected := map[pb.BackendKind]*pb.BackendGeneration{}
	for _, g := range manifest.BackendGenerations {
		if expected[g.Backend] != nil {
			return errors.New("duplicate backend generation")
		}
		expected[g.Backend] = g
	}
	for _, r := range manifest.Acknowledgements {
		g := expected[r.Backend]
		if g == nil {
			return errors.New("unexpected/duplicate backend receipt")
		}
		if r.PublicationId != manifest.Meta.RecordId || r.Fence != fence || r.Generation != g.Generation || !proto.Equal(r.OperationsChecksum, g.OperationsChecksum) || r.Counts.Expected != g.ExpectedCounts.Expected || r.Counts.Accepted != g.ExpectedCounts.Expected || r.Counts.Rejected != 0 || !r.DurableAck || !r.SearchReady {
			return errors.New("backend receipt not durably search-ready or mismatched")
		}
		delete(expected, r.Backend)
	}
	if len(expected) > 0 {
		return errors.New("publication missing backend receipts")
	}
	return nil
}

// SourceURLLookup must resolve trusted source metadata pinned to the bundle snapshot.
// It returns observed detail/download/final URLs; model-generated URLs are never accepted as metadata.
type SourceURLLookup func(sourceBlobID, provisionVersionID string) ([]string, error)

func VerifyCitationEvidence(answer *pb.Answer, bundle *pb.EvidenceBundle, lookup SourceURLLookup) error {
	if e := ValidateWire(answer, DefaultWireLimits); e != nil {
		return e
	}
	if e := ValidateWire(bundle, DefaultWireLimits); e != nil {
		return e
	}
	if !proto.Equal(answer.Snapshot, bundle.Snapshot) {
		return errors.New("answer/evidence snapshot mismatch")
	}
	evidence := map[string]*pb.Evidence{}
	for _, e := range bundle.Items {
		evidence[e.Meta.RecordId] = e
	}
	claims := map[string]*pb.Claim{}
	for _, c := range answer.Claims {
		claims[c.ClaimId] = c
		for _, id := range c.EvidenceIds {
			if evidence[id] == nil {
				return errors.New("claim references unknown evidence")
			}
		}
	}
	for _, citation := range answer.Citations {
		if lookup == nil {
			return errors.New("citation URL verification requires trusted source metadata")
		}
		item := evidence[citation.EvidenceId]
		if item == nil {
			return errors.New("citation references unknown evidence")
		}
		version := false
		for _, s := range item.SourceRefs {
			if s.ProvisionVersionId == citation.ProvisionVersionId {
				version = true
			}
		}
		if !version {
			return errors.New("citation version mismatch")
		}
		urlOK := false
		matchedSources := map[string]bool{}
		for _, source := range item.SourceRefs {
			if source.ProvisionVersionId == citation.ProvisionVersionId {
				urls, err := lookup(source.SourceBlobId, source.ProvisionVersionId)
				if err != nil {
					return err
				}
				for _, u := range urls {
					if u == citation.SourceUrl {
						urlOK = true
						matchedSources[source.SourceBlobId] = true
					}
				}
			}
		}
		if !urlOK {
			return errors.New("citation URL absent from trusted source metadata")
		}
		spanOK := citation.SourceSpan == nil
		for _, s := range item.SourceSpans {
			if citation.SourceSpan != nil && citation.SourceSpan.TextArtifactId == s.TextArtifactId && citation.SourceSpan.StartByte >= s.StartByte && citation.SourceSpan.EndByte <= s.EndByte {
				spanOK = true
			}
		}
		locatorOK := citation.PageLocator == nil
		for _, l := range item.Locators {
			if citation.PageLocator != nil && matchedSources[citation.PageLocator.SourceBlobId] && proto.Equal(l, citation.PageLocator) {
				locatorOK = true
			}
		}
		if !spanOK || !locatorOK {
			return errors.New("citation locator not in selected evidence")
		}
		for _, id := range citation.ClaimIds {
			linked := false
			for _, e := range claims[id].EvidenceIds {
				if e == citation.EvidenceId {
					linked = true
				}
			}
			if !linked {
				return errors.New("citation evidence absent from claim mapping")
			}
		}
	}
	return nil
}
