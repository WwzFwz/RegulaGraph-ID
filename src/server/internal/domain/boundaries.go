// Cross-record C01 guards bind worker/model/publication responses to their initiating context.
// Pure checks return errors before side effects; actual leases, permissions and database state come from S01.
// Use once per bounded batch, not once per graph edge. Required performance gates remain unmeasured.
package domain

import (
	"errors"
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
	if res.Status == pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED && (len(res.Errors) > 0 || res.DocumentBatch == nil && res.GraphDelta == nil && res.IndexBatch == nil) {
		return errors.New("successful worker response requires output and no errors")
	}
	return nil
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
