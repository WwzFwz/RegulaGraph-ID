// C01 boundary validation consumes generated Protobuf descriptors and shared field rules.
// Input is a bounded decoded message; errors identify invariant/field without dumping content.
// Cross-record checks supplement scalar rules; external registry/storage checks remain S01 owners.
// Limits protect memory/recursion; benchmark targets remain configs/benchmark-targets.yaml, unmeasured.
package domain

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type WireLimits struct {
	MaxBytes int
	MaxDepth int
	MaxItems int
}

var DefaultWireLimits = WireLimits{MaxBytes: 16 << 20, MaxDepth: 64, MaxItems: 100000}

func DecodeWire(raw []byte, dst proto.Message, limits WireLimits) error {
	if limits.MaxBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxItems <= 0 {
		return errors.New("invalid wire limits")
	}
	if len(raw) > limits.MaxBytes {
		return errors.New("wire payload exceeds byte limit")
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false, RecursionLimit: limits.MaxDepth}).Unmarshal(raw, dst); err != nil {
		return err
	}
	return ValidateWire(dst, limits)
}
func ValidateWire(message proto.Message, limits WireLimits) error {
	if message == nil || !message.ProtoReflect().IsValid() {
		return errors.New("nil wire message")
	}
	if limits.MaxBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxItems <= 0 {
		return errors.New("invalid wire limits")
	}
	if proto.Size(message) > limits.MaxBytes {
		return errors.New("wire payload exceeds byte limit")
	}
	remaining := limits.MaxItems
	var walk func(protoreflect.Message, int, string) error
	walk = func(m protoreflect.Message, depth int, corpus string) error {
		own := wireCorpus(m)
		if own != "" {
			if corpus != "" && corpus != own {
				return errors.New("nested corpus mismatch")
			}
			corpus = own
		}
		if depth > limits.MaxDepth {
			return errors.New("wire nesting exceeds depth limit")
		}
		fields := m.Descriptor().Fields()
		for i := 0; i < fields.Len(); i++ {
			f := fields.Get(i)
			opts := f.Options().(*descriptorpb.FieldOptions)
			r := proto.GetExtension(opts, pb.E_Rules).(*pb.FieldRules)
			if r == nil {
				r = &pb.FieldRules{}
			}
			where := string(f.FullName())
			v := m.Get(f)
			if r.Required && !m.Has(f) {
				return fmt.Errorf("%s: required", where)
			}
			values := []protoreflect.Value{}
			if f.IsList() {
				list := v.List()
				remaining -= list.Len()
				if remaining < 0 {
					return errors.New("wire item limit exceeded")
				}
				if list.Len() < int(r.MinItems) {
					return fmt.Errorf("%s: too few items", where)
				}
				for j := 0; j < list.Len(); j++ {
					values = append(values, list.Get(j))
				}
			} else if !f.HasPresence() || m.Has(f) {
				values = append(values, v)
			}
			seen := map[string]bool{}
			for _, x := range values {
				if r.Unique {
					key := fmt.Sprint(x.Interface())
					if seen[key] {
						return fmt.Errorf("%s: duplicate value", where)
					}
					seen[key] = true
				}
				if f.Kind() == protoreflect.MessageKind {
					remaining--
					if remaining < 0 {
						return errors.New("wire item limit exceeded")
					}
					if err := walk(x.Message(), depth+1, corpus); err != nil {
						return err
					}
					continue
				}
				if f.Kind() == protoreflect.StringKind {
					s := x.String()
					if !utf8.ValidString(s) {
						return fmt.Errorf("%s: invalid UTF-8", where)
					}
					if r.AsciiId && (len(s) == 0 || len(s) > 256 || strings.IndexFunc(s, func(c rune) bool { return c < 33 || c > 126 }) >= 0) {
						return fmt.Errorf("%s: invalid opaque ASCII ID", where)
					}
					if r.Sha256 && (len(s) != 64 || strings.IndexFunc(s, func(c rune) bool { return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') }) >= 0) {
						return fmt.Errorf("%s: invalid SHA-256", where)
					}
				}
				if f.Kind() == protoreflect.EnumKind && r.Required && (x.Enum() == 0 || f.Enum().Values().ByNumber(x.Enum()) == nil) {
					return fmt.Errorf("%s: invalid required enum", where)
				}
				if r.Positive || r.Finite || r.Probability {
					var n float64
					switch f.Kind() {
					case protoreflect.FloatKind, protoreflect.DoubleKind:
						n = x.Float()
					case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
						n = float64(x.Uint())
					default:
						n = float64(x.Int())
					}
					if math.IsNaN(n) || math.IsInf(n, 0) || (r.Positive && n <= 0) || (r.Probability && (n < 0 || n > 1)) {
						return fmt.Errorf("%s: numeric constraint", where)
					}
				}
				if f.Name() == "schema_version" && x.Uint() != 1 {
					return fmt.Errorf("%s: unsupported schema version", where)
				}
			}
		}
		oneofs := m.Descriptor().Oneofs()
		for i := 0; i < oneofs.Len(); i++ {
			o := oneofs.Get(i)
			if !o.IsSynthetic() && m.WhichOneof(o) == nil {
				return fmt.Errorf("%s: oneof required", o.FullName())
			}
		}
		return validateSemantics(m.Interface())
	}
	return walk(message.ProtoReflect(), 0, "")
}

func dateNumber(d *pb.CalendarDate) int64 {
	return int64(d.Year)*10000 + int64(d.Month)*100 + int64(d.Day)
}
func validateSemantics(message proto.Message) error {
	bad := func(s string) error { return fmt.Errorf("%s: %s", message.ProtoReflect().Descriptor().Name(), s) }
	switch m := message.(type) {
	case *timestamppb.Timestamp:
		if m.CheckValid() != nil {
			return bad("invalid timestamp")
		}
	case *pb.CalendarDate:
		if m.Year > 9999 || m.Month > 12 || m.Day > 31 {
			return bad("invalid calendar date")
		}
		d := time.Date(int(m.Year), time.Month(m.Month), int(m.Day), 0, 0, 0, 0, time.UTC)
		if d.Year() != int(m.Year) || d.Month() != time.Month(m.Month) || d.Day() != int(m.Day) {
			return bad("invalid calendar date")
		}
	case *pb.DateAssertion:
		if m.Knowledge == pb.DateKnowledge_DATE_KNOWLEDGE_KNOWN {
			if m.Value == nil || len(m.Alternatives) > 0 {
				return bad("known date requires one value")
			}
		} else if m.Value != nil {
			return bad("unknown/conflict/unbounded cannot have a resolved value")
		}
		if m.Knowledge == pb.DateKnowledge_DATE_KNOWLEDGE_CONFLICT {
			if len(m.Alternatives) < 2 {
				return bad("conflict requires alternatives")
			}
		} else if len(m.Alternatives) > 0 {
			return bad("alternatives require conflict status")
		}
	case *pb.LegalInterval:
		if m.Start.Value != nil && m.End.Value != nil && dateNumber(m.Start.Value) >= dateNumber(m.End.Value) {
			return bad("empty or reversed legal interval")
		}
	case *pb.Visibility:
		if m.ToSeq != nil && *m.ToSeq <= m.FromSeq {
			return bad("invalid visibility interval")
		}
	case *pb.TextSpan:
		if m.EndByte < m.StartByte {
			return bad("reversed byte span")
		}
	case *pb.AnswerTextSpan:
		if m.EndByte < m.StartByte {
			return bad("reversed answer span")
		}
	case *pb.BoundingBox:
		if m.X0 > m.X1 || m.Y0 > m.Y1 {
			return bad("reversed bounding box")
		}
	case *pb.ArtifactRef:
		if strings.Contains(m.StorageKey, "\\") || strings.HasPrefix(m.StorageKey, "/") || path.Clean(m.StorageKey) != m.StorageKey || strings.HasPrefix(m.StorageKey, "../") || (m.StorageKey == ".." || m.StorageKey == ".") || strings.Contains(m.StorageKey, ":") {
			return bad("storage key must be a relative artifact key")
		}
	case *pb.RequestContext:
		if m.Deadline.CheckValid() != nil {
			return bad("invalid deadline timestamp")
		}
		if m.SnapshotRef != nil && m.CorpusId != m.SnapshotRef.CorpusId {
			return bad("context/snapshot corpus mismatch")
		}
	case *pb.SourceObservation:
		if m.FetchedAt.CheckValid() != nil {
			return bad("invalid observation timestamp")
		}
		if m.Status == pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE && (m.SourceBlobId == nil || m.Error != nil) {
			return bad("complete observation requires blob and no error")
		}
	case *pb.SourceBlob:
		if m.ByteSize != m.ArtifactRef.ByteSize || m.RawSha256.Sha256 != m.ArtifactRef.ContentHash.Sha256 {
			return bad("blob/artifact mismatch")
		}
	case *pb.TemporalScope:
		if m.Mode == pb.TemporalMode_TEMPORAL_MODE_AS_OF && m.EffectiveAt == nil {
			return bad("AS_OF requires effective date")
		}
		if m.Mode == pb.TemporalMode_TEMPORAL_MODE_COMPARE {
			if len(m.CompareDates) < 2 || m.EffectiveAt != nil {
				return bad("COMPARE requires date list")
			}
		} else if len(m.CompareDates) > 0 {
			return bad("compare dates outside COMPARE")
		}
	case *pb.DenseVector:
		if len(m.Values) != int(m.Dimensions) {
			return bad("dense dimension mismatch")
		}
	case *pb.SparseVector:
		if len(m.Indices) != len(m.Values) {
			return bad("sparse length mismatch")
		}
		for i := 1; i < len(m.Indices); i++ {
			if m.Indices[i] <= m.Indices[i-1] {
				return bad("sparse indices not strictly sorted")
			}
		}
	case *pb.Counts:
		if m.Accepted > m.Expected || m.Rejected != m.Expected-m.Accepted {
			return bad("counts do not balance")
		}
	case *pb.TruncationInfo:
		if m.RetainedTokens > m.OriginalTokens || (!m.Truncated && m.RetainedTokens != m.OriginalTokens) {
			return bad("inconsistent truncation")
		}
	case *pb.GraphPath:
		if len(m.OrderedNodeIds) != len(m.OrderedAssertionIds)+1 || len(m.SelectedSupportIds) != len(m.OrderedAssertionIds) {
			return bad("path edge/node/support cardinality mismatch")
		}
	case *pb.EvidenceBundle:
		ids := map[string]bool{}
		for _, v := range m.Items {
			if ids[v.Meta.RecordId] {
				return bad("duplicate evidence ID")
			}
			ids[v.Meta.RecordId] = true
			if !proto.Equal(v.SnapshotRef, m.Snapshot) || v.Meta.CorpusId != m.Meta.CorpusId {
				return bad("mixed evidence snapshots/corpora")
			}
		}
		if m.Completeness == pb.Completeness_COMPLETENESS_COMPLETE && (len(m.MissingDependencies) > 0 || m.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) {
			return bad("incomplete evidence marked complete")
		}
	case *pb.ContextBundle:
		if len(m.OrderedEvidenceIds) != len(m.RenderedBlocks) {
			return bad("context block mismatch")
		}
		for i, b := range m.RenderedBlocks {
			if b.EvidenceId != m.OrderedEvidenceIds[i] {
				return bad("context order mismatch")
			}
		}
		if m.Completeness == pb.Completeness_COMPLETENESS_COMPLETE && len(m.OmittedRequiredRefs) > 0 {
			return bad("omitted evidence marked complete")
		}
	case *pb.Citation:
		if m.SourceSpan == nil && m.PageLocator == nil {
			return bad("citation requires locator")
		}
		u, e := url.Parse(m.SourceUrl)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return bad("invalid citation URL")
		}
	case *pb.Answer:
		if m.Meta.CorpusId != m.Snapshot.CorpusId {
			return bad("answer corpus mismatch")
		}
		claims := map[string]bool{}
		for _, c := range m.Claims {
			if claims[c.ClaimId] {
				return bad("duplicate claim")
			}
			claims[c.ClaimId] = true
			if e := CheckUTF8Span([]byte(m.Text), c.AnswerTextSpan.StartByte, c.AnswerTextSpan.EndByte); e != nil {
				return e
			}
		}
		for _, c := range m.Citations {
			for _, id := range c.ClaimIds {
				if !claims[id] {
					return bad("citation references unknown claim")
				}
			}
		}
		if m.SemanticStatus == pb.SemanticStatus_SEMANTIC_STATUS_COMPLETE && (len(m.MissingEvidence) > 0 || m.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) {
			return bad("incomplete answer marked complete")
		}
	case *pb.AnswerEvent:
		if f := m.GetFinal(); f != nil && (f.RequestId != m.RequestId || f.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED) {
			return bad("invalid FINAL event")
		}
	case *pb.UpdatePlan:
		seen := map[string]bool{}
		for _, id := range m.ReuseSet {
			seen[id] = true
		}
		for _, id := range m.RecomputeSet {
			if seen[id] {
				return bad("reuse/recompute overlap")
			}
		}
	case *pb.ModelManifest:
		if m.Task == pb.ModelTask_MODEL_TASK_EMBED && m.Dimensions == nil {
			return bad("embedding requires dimensions")
		}
	case *pb.EmbedBatchRequest:
		if m.Model.Task != pb.ModelTask_MODEL_TASK_EMBED {
			return bad("wrong embedding model task")
		}
		ids := map[string]bool{}
		for _, x := range m.Items {
			if ids[x.ItemId] {
				return bad("duplicate batch item ID")
			}
			ids[x.ItemId] = true
		}
	case *pb.RerankBatchRequest:
		if m.Model.Task != pb.ModelTask_MODEL_TASK_RERANK {
			return bad("wrong reranker model task")
		}
		ids := map[string]bool{}
		for _, x := range m.Pairs {
			if ids[x.PairId] {
				return bad("duplicate pair ID")
			}
			ids[x.PairId] = true
		}
	case *pb.GateResult:
		if m.Status == pb.GateStatus_GATE_STATUS_PASS && (m.Denominator == 0 || m.Estimate == nil || len(m.RawArtifacts) == 0) {
			return bad("PASS requires samples, estimate, raw evidence")
		}
	}
	return nil
}

// CheckUTF8Span must be called after the referenced text artifact has been resolved.
func CheckUTF8Span(text []byte, start, end uint64) error {
	if !utf8.Valid(text) || start > end || end > uint64(len(text)) {
		return errors.New("invalid UTF-8 text/span bounds")
	}
	if start < uint64(len(text)) && !utf8.RuneStart(text[start]) || end < uint64(len(text)) && !utf8.RuneStart(text[end]) {
		return errors.New("span splits UTF-8 code point")
	}
	return nil
}

// VerifyEmbeddingResults rejects missing/extra/duplicate IDs and model changes across a batch.
func VerifyEmbeddingResults(req *pb.EmbedBatchRequest, res *pb.EmbedBatchResponse) error {
	return VerifyEmbeddingResultsWithLimits(req, res, DefaultWireLimits)
}

// VerifyEmbeddingResultsWithLimits lets native callers account for every vector scalar
// within their explicit byte/item budget without duplicating correlation rules.
func VerifyEmbeddingResultsWithLimits(req *pb.EmbedBatchRequest, res *pb.EmbedBatchResponse, limits WireLimits) error {
	if err := ValidateWire(req, DefaultWireLimits); err != nil {
		return err
	}
	if err := ValidateWire(res, limits); err != nil {
		return err
	}
	if req.Context.RequestId != res.RequestId || !proto.Equal(req.Model, res.Model) {
		return errors.New("batch response context/model mismatch")
	}
	expected := map[string]bool{}
	for _, x := range req.Items {
		expected[x.ItemId] = true
	}
	for _, x := range res.Results {
		if !expected[x.ItemId] {
			return errors.New("unexpected/duplicate result ID")
		}
		delete(expected, x.ItemId)
		if v := x.GetEmbedding(); v != nil && uint32(len(v.Values)) != req.Model.GetDimensions() {
			return errors.New("embedding result dimension mismatch")
		}
	}
	if len(expected) > 0 {
		return errors.New("batch results missing IDs")
	}
	return nil
}

type AnswerStreamValidator struct {
	RequestID string
	Sequence  uint64
	Terminal  bool
}

func (s *AnswerStreamValidator) Accept(event *pb.AnswerEvent) error {
	if s.Terminal {
		return errors.New("event after terminal")
	}
	if err := ValidateWire(event, DefaultWireLimits); err != nil {
		return err
	}
	if s.RequestID != "" && s.RequestID != event.RequestId || event.StreamSequence != s.Sequence+1 {
		return errors.New("stream correlation/sequence mismatch")
	}
	s.RequestID = event.RequestId
	s.Sequence = event.StreamSequence
	s.Terminal = event.GetFinal() != nil || event.GetError() != nil
	return nil
}
func (s *AnswerStreamValidator) Finish() error {
	if !s.Terminal {
		return errors.New("stream ended without terminal event")
	}
	return nil
}

// EqualPayload compares idempotency fingerprints, not raw non-canonical Protobuf encoding.
func EqualPayload(a, b *pb.ContentHash) bool {
	return a != nil && b != nil && bytes.Equal([]byte(a.Sha256), []byte(b.Sha256))
}

// Infer only explicitly present corpus context; absence is checked by declared required rules.
func wireCorpus(m protoreflect.Message) string {
	if f := m.Descriptor().Fields().ByName("corpus_id"); f != nil {
		return m.Get(f).String()
	}
	for _, name := range []protoreflect.Name{"meta", "context", "batch"} {
		if f := m.Descriptor().Fields().ByName(name); f != nil && f.Kind() == protoreflect.MessageKind && m.Has(f) {
			if c := wireCorpus(m.Get(f).Message()); c != "" {
				return c
			}
		}
	}
	return ""
}
