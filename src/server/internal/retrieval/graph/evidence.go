// Mengubah hasil traversal menjadi Evidence beserta rantai relasi dan teks sumber.
//
// Peran dalam komponen:
// Menjembatani graph retrieval ke fusion dan context builder.
//
// Kontrak integrasi dan perhatian implementasi:
// Pasal penghubung tidak dibuang hanya karena skor individual rendah; deduplikasi mempertahankan jalur dan semua provenance yang diperlukan.
//
// Benchmark dan gate penerimaan:
// [RETRIEVAL] Ukur Recall@k, nDCG@k, kelengkapan semua bukti multi-hop, serta latency p50/p95/p99 per jenis pertanyaan. Catat jumlah kandidat, hop, snapshot, dan kondisi cold/warm. Ambang wajib mengikuti configs/benchmark-targets.yaml; jangan menganggap batas hop/kandidat atau graph selalu meningkatkan kualitas.
//
// [CONTEXT] Ukur cakupan gold evidence, kelengkapan jalur graph, duplikasi, token count, dan waktu membangun konteks. Gate: tiap item konteks dapat dipetakan ke sumber dan versi; pemotongan/ketidakcukupan bukti dilaporkan. Context budget tidak boleh diam-diam menghapus syarat penting.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: pemetaan support/path ke Evidence hasil hidrasi sumber tersedia.
// Caller wajib mengautentikasi teks/katalog dan menjalankan filter temporal sumber.
// Gap span, sumber salah, exception/qualifier dan temporal assertion yang belum
// terselesaikan tetap dilaporkan; mapping tidak menganggap model fact sebagai gold.
// Bukti verifikasi: Test unsupported edges, stale versions and source mismatch; profile database round trips and required-path coverage.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package graph

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type EvidenceMapping struct {
	Bundle          *pb.EvidenceBundle
	SupportEvidence map[string][]string
	// Every listed item is required; one item's copy of GraphPath is not proof
	// that the remaining supporting text survived context truncation.
	PathEvidence map[string][]string
}

// SourcesForPaths retains alternate supports of every walked assertion; it does
// not gather unrelated expansion branches. Output is stable and deduplicated.
func SourcesForPaths(traversal *TraversalResult, maximumSources int) ([]*pb.SourceVersionRef, error) {
	supports, err := pathSupports(traversal)
	if err != nil {
		return nil, err
	}
	if maximumSources < 1 || maximumSources > 128 {
		return nil, errors.New("bounded graph source selection required")
	}
	refs := map[string]*pb.SourceVersionRef{}
	for _, s := range supports {
		for _, ref := range s.SourceRefs {
			refs[sourceKey(ref)] = ref
		}
	}
	if len(refs) > maximumSources {
		return nil, domain.ErrGraphReadBudget
	}
	keys := sortedKeys(refs)
	out := make([]*pb.SourceVersionRef, 0, len(keys))
	for _, key := range keys {
		out = append(out, proto.Clone(refs[key]).(*pb.SourceVersionRef))
	}
	return out, nil
}

// AttachEvidence consumes authenticated, AS_OF-filtered SourceHydrator output.
// It proves byte-span/source coverage, not semantic entailment. All path text is
// retained; a later context builder must account for omitted multi-hop evidence.
func AttachEvidence(traversal *TraversalResult, request *pb.QuestionRequest, hydrated *pb.EvidenceBundle, maximumBytes uint64) (*EvidenceMapping, error) {
	supports, err := pathSupports(traversal)
	if err != nil {
		return nil, err
	}
	if request == nil || hydrated == nil || maximumBytes < 1 || maximumBytes > 4<<20 || domain.ValidateWire(request, domain.DefaultWireLimits) != nil || domain.ValidateWire(hydrated, domain.DefaultWireLimits) != nil || !proto.Equal(hydrated.Snapshot, traversal.Snapshot) || request.CorpusId != traversal.Snapshot.CorpusId || request.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || request.TemporalScope.EffectiveAt == nil || hydrated.CompletionStatus != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
		return nil, errors.New("matching authenticated graph evidence and AS_OF request required")
	}
	if request.SnapshotId != nil && *request.SnapshotId != traversal.Snapshot.SnapshotId || request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, traversal.Snapshot) {
		return nil, errors.New("graph evidence request snapshot differs")
	}
	if hydrated.Meta.CorpusId != traversal.Snapshot.CorpusId || uint64(proto.Size(hydrated)) > maximumBytes {
		return nil, errors.New("hydrated graph bundle scope or size mismatch")
	}
	bundle := proto.Clone(hydrated).(*pb.EvidenceBundle)
	remaining := maximumBytes - uint64(proto.Size(bundle))
	byID := map[string]*pb.Evidence{}
	for _, e := range bundle.Items {
		if e.Meta.CorpusId != traversal.Snapshot.CorpusId || !proto.Equal(e.SnapshotRef, traversal.Snapshot) || byID[e.Meta.RecordId] != nil || len(e.SourceSpans) != 1 || e.SourceSpans[0].EndByte-e.SourceSpans[0].StartByte != uint64(len(e.Text)) || !utf8.ValidString(e.Text) {
			return nil, errors.New("graph mapping requires unique, exact single-span source text")
		}
		byID[e.Meta.RecordId] = e
	}
	result := &EvidenceMapping{Bundle: bundle, SupportEvidence: map[string][]string{}, PathEvidence: map[string][]string{}}
	missing := map[string]bool{}
	for _, id := range bundle.MissingDependencies {
		missing[id] = true
	}
	completeSupport := map[string]bool{}
	assertionItems := map[string]map[string]bool{}
	for _, id := range sortedKeys(supports) {
		s := supports[id]
		if s.ReviewState == pb.ReviewState_REVIEW_STATE_REJECTED {
			missing[id] = true
			continue
		}
		items, complete, e := matchSupport(s, byID)
		if e != nil {
			return nil, e
		}
		result.SupportEvidence[id] = items
		if assertionItems[s.AssertionId] == nil {
			assertionItems[s.AssertionId] = map[string]bool{}
		}
		for _, item := range items {
			assertionItems[s.AssertionId][item] = true
		}
		completeSupport[id] = complete
		if !complete {
			missing[id] = true
		}
	}
	used := map[string]bool{}
	for _, discovered := range traversal.Paths {
		path := proto.Clone(discovered).(*pb.GraphPath)
		path.Coverage = pb.Completeness_COMPLETENESS_COMPLETE
		pathItems := map[string]bool{}
		for _, assertionID := range path.OrderedAssertionIds {
			a := traversal.Assertions[assertionID]
			for item := range assertionItems[assertionID] {
				pathItems[item] = true
			}
			for _, id := range a.ExceptionRefs {
				missing[id] = true
				path.Coverage = pb.Completeness_COMPLETENESS_PARTIAL
			}
			for _, q := range a.Qualifiers {
				if q.GetCanonicalId() != "" || q.GetMentionId() != "" {
					missing[dependencyID("graph-qualifier", assertionID)] = true
					path.Coverage = pb.Completeness_COMPLETENESS_PARTIAL
				}
			}
			if a.Origin == pb.AssertionOrigin_ASSERTION_ORIGIN_INFERRED || a.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || !proto.Equal(a.TemporalScope.EffectiveAt, request.TemporalScope.EffectiveAt) || a.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(a.TemporalScope.KnowledgeSnapshot, traversal.Snapshot) {
				missing[dependencyID("graph-applicability", assertionID)] = true
				path.Coverage = pb.Completeness_COMPLETENESS_PARTIAL
			}
		}
		for _, id := range path.SelectedSupportIds {
			if !completeSupport[id] {
				path.Coverage = pb.Completeness_COMPLETENESS_PARTIAL
			}
		}
		result.PathEvidence[path.PathId] = sortedKeys(pathItems)
		for _, id := range result.PathEvidence[path.PathId] {
			if byID[id].LegalStatus == pb.LegalStatus_LEGAL_STATUS_UNKNOWN || byID[id].LegalStatus == pb.LegalStatus_LEGAL_STATUS_CONFLICT {
				path.Coverage = pb.Completeness_COMPLETENESS_PARTIAL
			}
		}
		for _, id := range result.PathEvidence[path.PathId] {
			cost := uint64(1 + protowire.SizeBytes(proto.Size(path)))
			if cost > remaining {
				return nil, domain.ErrGraphReadBudget
			}
			remaining -= cost
			byID[id].GraphPaths = append(byID[id].GraphPaths, proto.Clone(path).(*pb.GraphPath))
			used[id] = true
		}
		required := &pb.RequiredPathSet{PathIds: []string{path.PathId}}
		cost := uint64(2 + protowire.SizeBytes(proto.Size(required)))
		if cost > remaining {
			return nil, domain.ErrGraphReadBudget
		}
		remaining -= cost
		bundle.RequiredPathSets = append(bundle.RequiredPathSets, required)
		if path.Coverage != pb.Completeness_COMPLETENESS_COMPLETE || len(pathItems) == 0 {
			missing[path.PathId] = true
		}
	}
	for _, reason := range traversal.StopReasons {
		missing[dependencyID("graph-frontier", reason)] = true
	}
	if !traversal.FrontierExhausted {
		missing[dependencyID("graph-frontier", traversal.Snapshot.SnapshotId)] = true
	}
	bundle.Items = nil
	for _, id := range sortedKeys(used) {
		bundle.Items = append(bundle.Items, byID[id])
	}
	bundle.MissingDependencies = sortedKeys(missing)
	if len(missing) > 0 || bundle.Completeness == pb.Completeness_COMPLETENESS_PARTIAL {
		bundle.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	} else if len(bundle.Items) == 0 {
		bundle.Completeness = pb.Completeness_COMPLETENESS_NONE
	}
	if uint64(proto.Size(bundle)) > maximumBytes {
		return nil, domain.ErrGraphReadBudget
	}
	if err = domain.ValidateWire(bundle, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	return result, nil
}

func pathSupports(t *TraversalResult) (map[string]*pb.SupportRecord, error) {
	if t == nil || t.Snapshot == nil || domain.ValidateWire(t.Snapshot, domain.DefaultWireLimits) != nil || len(t.Paths) > 4096 {
		return nil, errors.New("bounded graph traversal required")
	}
	selected := map[string]bool{}
	paths := map[string]bool{}
	var bytes uint64
	for _, p := range t.Paths {
		if p == nil || domain.ValidateWire(p, domain.DefaultWireLimits) != nil || !proto.Equal(p.Snapshot, t.Snapshot) || paths[p.PathId] {
			return nil, errors.New("invalid or duplicate discovery path")
		}
		paths[p.PathId] = true
		bytes += uint64(proto.Size(p))
		if bytes > 16<<20 {
			return nil, domain.ErrGraphReadBudget
		}
		for i, id := range p.OrderedAssertionIds {
			a := t.Assertions[id]
			s := t.Supports[p.SelectedSupportIds[i]]
			if a == nil || s == nil || s.AssertionId != id || (a.SubjectId != p.OrderedNodeIds[i] || a.ObjectId != p.OrderedNodeIds[i+1]) && (a.ObjectId != p.OrderedNodeIds[i] || a.SubjectId != p.OrderedNodeIds[i+1]) {
				return nil, errors.New("path lost assertion direction or support identity")
			}
			if !selected[id] {
				if domain.ValidateWire(a, domain.DefaultWireLimits) != nil || a.Meta.RecordId != id || !graphEvidenceMeta(a.Meta, t.Snapshot) {
					return nil, errors.New("invalid graph assertion")
				}
				bytes += uint64(proto.Size(a))
				if bytes > 16<<20 {
					return nil, domain.ErrGraphReadBudget
				}
			}
			selected[id] = true
		}
	}
	result := map[string]*pb.SupportRecord{}
	for id, s := range t.Supports {
		if s == nil {
			return nil, errors.New("nil graph support")
		}
		if !selected[s.AssertionId] {
			continue
		}
		if domain.ValidateWire(s, domain.DefaultWireLimits) != nil || s.Meta.RecordId != id || !graphEvidenceMeta(s.Meta, t.Snapshot) {
			return nil, errors.New("invalid graph support")
		}
		bytes += uint64(proto.Size(s))
		if bytes > 16<<20 {
			return nil, domain.ErrGraphReadBudget
		}
		result[id] = s
	}
	return result, nil
}

func graphEvidenceMeta(meta *pb.RecordMeta, snapshot *pb.SnapshotRef) bool {
	return meta != nil && meta.SchemaVersion == 1 && meta.CorpusId == snapshot.CorpusId && meta.Visibility != nil && meta.Visibility.FromSeq == snapshot.Sequence && meta.Visibility.ToSeq == nil
}

func matchSupport(s *pb.SupportRecord, items map[string]*pb.Evidence) ([]string, bool, error) {
	refs := map[string]bool{}
	for _, ref := range s.SourceRefs {
		refs[sourceKey(ref)] = true
	}
	coveredRefs := map[string]bool{}
	matched := map[string]bool{}
	complete := true
	for _, span := range s.EvidenceSpans {
		type interval struct{ start, end uint64 }
		var ranges []interval
		for id, e := range items {
			present := false
			for _, ref := range e.SourceRefs {
				present = present || refs[sourceKey(ref)]
			}
			if !present {
				continue
			}
			part := e.SourceSpans[0]
			if part.TextArtifactId != span.TextArtifactId || part.StartByte >= span.EndByte || part.EndByte <= span.StartByte {
				continue
			}
			start, end := max(part.StartByte, span.StartByte), min(part.EndByte, span.EndByte)
			if !utf8.ValidString(e.Text[start-part.StartByte : end-part.StartByte]) {
				return nil, false, errors.New("support span splits UTF-8 source text")
			}
			ranges = append(ranges, interval{start, end})
			matched[id] = true
			for _, ref := range e.SourceRefs {
				key := sourceKey(ref)
				if refs[key] {
					coveredRefs[key] = true
				}
			}
		}
		sort.Slice(ranges, func(i, j int) bool {
			if ranges[i].start == ranges[j].start {
				return ranges[i].end < ranges[j].end
			}
			return ranges[i].start < ranges[j].start
		})
		cursor := span.StartByte
		for _, part := range ranges {
			if part.start > cursor {
				break
			}
			cursor = max(cursor, part.end)
		}
		if cursor < span.EndByte {
			complete = false
		}
	}
	return sortedKeys(matched), complete && len(coveredRefs) == len(refs), nil
}

func sourceKey(ref *pb.SourceVersionRef) string {
	return ref.SourceBlobId + "\x00" + ref.ProvisionVersionId + "\x00" + ref.RegulationId
}
func dependencyID(prefix, value string) string {
	return fmt.Sprintf("%s:%x", prefix, sha256.Sum256([]byte(value)))
}
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
