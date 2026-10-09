// Encodes an explicit COMPARE evidence response for HTTP and operator CLI.
// Every date retains its C01 EvidenceBundle, omissions, rejection accounting and
// reranking diagnostics. Enforce date order, full snapshot identity and byte
// budgets before writing; malformed/partial operations never become success.
// This envelope adds no competing evidence schema and makes no quality claim.
// Measure serialization bytes/latency under configs/benchmark-targets.yaml.
package schemas

import (
	"encoding/json"
	"errors"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/retrieval/query"
	"regulagraph.local/server/internal/workflows"
)

const MaximumComparisonJSONBytes = 16 << 20

type comparisonDate struct {
	Date      json.RawMessage   `json:"effective_date"`
	Evidence  json.RawMessage   `json:"evidence"`
	Rejected  map[string]string `json:"rejected"`
	Reranking *Reranking        `json:"reranking,omitempty"`
}

// MarshalEvidenceComparison validates returned output against the exact request.
// requiredReranking comes from operator configuration, never from output presence.
func MarshalEvidenceComparison(request *pb.QuestionRequest, run *workflows.RAGResult, requiredReranking bool) ([]byte, error) {
	if request == nil || run == nil || run.Comparison == nil || run.Evidence != nil || run.Answer != nil || run.Search != nil || run.Reranking != nil || run.Temporal != nil {
		return nil, errors.New("exclusive comparison output required")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if request.ResponseMode != pb.ResponseMode_RESPONSE_MODE_COMPLETE {
		return nil, errors.New("complete comparison response required")
	}
	plans, err := query.PlanComparisonScopes(request.TemporalScope)
	if err != nil {
		return nil, err
	}
	comparison := run.Comparison
	if comparison.Profile != request.RequestedProfile || comparison.Profile < pb.RetrievalProfile_RETRIEVAL_PROFILE_VECTOR_RAG || comparison.Profile > pb.RetrievalProfile_RETRIEVAL_PROFILE_HYBRID_GRAPH_RAG || len(comparison.Dates) != len(plans) || comparison.Duration < 0 {
		return nil, errors.New("comparison profile/date cardinality mismatch")
	}
	if err := domain.ValidateWire(comparison.Snapshot, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if comparison.Snapshot.CorpusId != request.CorpusId || request.SnapshotId != nil && *request.SnapshotId != comparison.Snapshot.SnapshotId || request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, comparison.Snapshot) {
		return nil, errors.New("comparison corpus/snapshot differs from request")
	}
	snapshot, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(comparison.Snapshot)
	if err != nil {
		return nil, err
	}
	out := struct {
		Mode     string           `json:"mode"`
		Profile  string           `json:"profile"`
		Snapshot json.RawMessage  `json:"snapshot"`
		Dates    []comparisonDate `json:"dates"`
	}{Mode: "evidence_comparison", Profile: comparison.Profile.String(), Snapshot: snapshot, Dates: make([]comparisonDate, 0, len(plans))}
	remaining := workflows.MaximumComparisonEvidenceBytes
	jsonRemaining := MaximumComparisonJSONBytes
	seenBundles := map[string]bool{}
	for i, dated := range comparison.Dates {
		result := dated.Result
		if !proto.Equal(dated.Date, plans[i].EffectiveAt) || result == nil || result.Comparison != nil || result.Answer != nil || result.Evidence == nil || result.Search == nil || result.Search.Profile != request.RequestedProfile || !proto.Equal(result.Search.Snapshot, comparison.Snapshot) || result.Search.Normalization == nil || result.Search.Normalization.Original != request.Question {
			return nil, errors.New("comparison bucket identity/date differs from request")
		}
		if err := query.ValidateTemporalResolution(plans[i], result.Temporal); err != nil {
			return nil, err
		}
		bundle := result.Evidence
		if err := domain.ValidateWire(bundle, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if bundle.Meta.CorpusId != request.CorpusId || !proto.Equal(bundle.Snapshot, comparison.Snapshot) || bundle.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED || seenBundles[bundle.Meta.RecordId] {
			return nil, errors.New("comparison evidence scope/status or bundle identity mismatch")
		}
		seenBundles[bundle.Meta.RecordId] = true
		seenItems := map[string]bool{}
		for _, item := range bundle.Items {
			if item.Meta.CorpusId != request.CorpusId || !proto.Equal(item.SnapshotRef, comparison.Snapshot) {
				return nil, errors.New("comparison item belongs to another scope")
			}
			if seenItems[item.Meta.RecordId] {
				return nil, errors.New("duplicate comparison evidence item")
			}
			if _, rejected := result.Rejected[item.Meta.RecordId]; rejected {
				return nil, errors.New("accepted comparison evidence also rejected")
			}
			seenItems[item.Meta.RecordId] = true
		}
		size := proto.Size(bundle)
		if size > remaining {
			return nil, errors.New("comparison evidence byte budget exceeded")
		}
		remaining -= size
		if len(result.Rejected) > 256 {
			return nil, errors.New("comparison rejection count exceeded")
		}
		for id, reason := range result.Rejected {
			if len(id) == 0 || len(id) > 1024 || len(reason) > 4096 || strings.TrimSpace(reason) == "" {
				return nil, errors.New("invalid comparison rejection diagnostic")
			}
		}
		reranking, err := encodeRerankingBudget(result, requiredReranking, jsonRemaining)
		if err != nil {
			return nil, err
		}
		dateJSON, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(dated.Date)
		if err != nil {
			return nil, err
		}
		evidenceJSON, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(bundle)
		if err != nil {
			return nil, err
		}
		bucket := comparisonDate{Date: dateJSON, Evidence: evidenceJSON, Rejected: result.Rejected, Reranking: reranking}
		bucketJSON, err := json.Marshal(bucket)
		if err != nil {
			return nil, err
		}
		if len(bucketJSON) > jsonRemaining {
			return nil, errors.New("comparison JSON byte budget exceeded")
		}
		jsonRemaining -= len(bucketJSON)
		out.Dates = append(out.Dates, bucket)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaximumComparisonJSONBytes {
		return nil, errors.New("comparison JSON byte budget exceeded")
	}
	return encoded, nil
}
