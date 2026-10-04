// Hydrates Qdrant candidates from a live pinned PostgreSQL catalog and immutable
// INDEX plan/source/text artifacts. Backend payloads never become evidence text.
// Artifact reads and source views are reused within one request, with explicit
// byte/item budgets and exact candidate accounting. This first hydrator retains
// unresolved parent/exception references as missing dependencies, never silently
// claiming complete context. Measure hydration p95/p99, bytes, RSS, exclusions
// and citation coverage under configs/benchmark-targets.yaml (UNMEASURED).
package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/adapters/qdrant"
	"regulagraph.local/server/internal/domain"
)

type EvidenceCatalog interface {
	LoadPinnedIndex(context.Context, domain.SnapshotPin) (*domain.PinnedIndex, error)
	LoadPinnedIndexRecords(context.Context, domain.SnapshotPin, []string, uint64) ([]domain.IndexCatalogRecord, error)
	LoadArtifact(context.Context, string, string) (*pb.ArtifactRef, error)
}
type EvidenceArtifactReader interface {
	ReadVerified(context.Context, *pb.ArtifactRef, uint64) ([]byte, error)
}
type HydrationConfig struct {
	MaximumCandidates                          int
	MaximumArtifactBytes, MaximumEvidenceBytes uint64
	Producer                                   *pb.ProducerManifest
}
type HydratedEvidence struct {
	Evidence   *pb.EvidenceBundle
	Rejected   map[string]string
	SourceURLs domain.SourceURLLookup
}
type SourceHydrator struct {
	catalog  EvidenceCatalog
	reader   EvidenceArtifactReader
	pin      domain.SnapshotPin
	snapshot *pb.SnapshotRef
	config   HydrationConfig
}

func NewSourceHydrator(catalog EvidenceCatalog, reader EvidenceArtifactReader, index *domain.PinnedIndex, config HydrationConfig) (*SourceHydrator, error) {
	if catalog == nil || reader == nil || index == nil || index.Snapshot == nil || config.Producer == nil || config.MaximumCandidates <= 0 || config.MaximumCandidates > 256 ||
		config.MaximumArtifactBytes == 0 || config.MaximumArtifactBytes > 64<<20 || config.MaximumEvidenceBytes == 0 || config.MaximumEvidenceBytes > 4<<20 {
		return nil, errors.New("trusted pinned index and bounded hydration configuration required")
	}
	if err := domain.ValidateWire(config.Producer, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if err := domain.ValidateWire(index.Snapshot, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if index.Pin.CorpusID != index.Snapshot.CorpusId || index.Pin.SnapshotID != index.Snapshot.SnapshotId || index.Pin.Sequence != index.Snapshot.Sequence {
		return nil, errors.New("hydration pin/snapshot mismatch")
	}
	config.Producer = proto.Clone(config.Producer).(*pb.ProducerManifest)
	return &SourceHydrator{catalog: catalog, reader: reader, pin: index.Pin, snapshot: proto.Clone(index.Snapshot).(*pb.SnapshotRef), config: config}, nil
}

func (h *SourceHydrator) Hydrate(ctx context.Context, request *pb.QuestionRequest, hits []qdrant.Hit) (*HydratedEvidence, error) {
	if h == nil || ctx == nil || request == nil || len(hits) > h.config.MaximumCandidates {
		return nil, errors.New("bounded hydration request required")
	}
	if err := domain.ValidateWire(request, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if request.CorpusId != h.snapshot.CorpusId || request.TemporalScope == nil || request.TemporalScope.Mode != pb.TemporalMode_TEMPORAL_MODE_AS_OF || request.TemporalScope.EffectiveAt == nil ||
		len(request.TemporalScope.CompareDates) != 0 || request.SnapshotId != nil && *request.SnapshotId != h.snapshot.SnapshotId ||
		request.TemporalScope.KnowledgeSnapshot != nil && !proto.Equal(request.TemporalScope.KnowledgeSnapshot, h.snapshot) {
		return nil, errors.New("hydration requires the admitted snapshot and an explicit AS_OF date")
	}
	bounded, cancel := context.WithDeadline(ctx, h.pin.ExpiresAt)
	defer cancel()
	index, err := h.catalog.LoadPinnedIndex(bounded, h.pin)
	if err != nil {
		return nil, err
	}
	if index == nil || !proto.Equal(index.Snapshot, h.snapshot) {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateIndexCatalogBinding(index.Binding); err != nil {
		return nil, err
	}
	ids := make([]string, len(hits))
	seen := map[string]bool{}
	for i, hit := range hits {
		if seen[hit.RecordID] {
			return nil, errors.New("duplicate hydration candidate")
		}
		seen[hit.RecordID] = true
		ids[i] = hit.RecordID
	}
	records, err := h.catalog.LoadPinnedIndexRecords(bounded, h.pin, ids, 4<<20)
	if err != nil {
		return nil, err
	}
	if len(records) != len(hits) {
		return nil, errors.New("incomplete catalog response")
	}
	l := &evidenceLoader{catalog: h.catalog, reader: h.reader, corpus: h.snapshot.CorpusId, remaining: h.config.MaximumArtifactBytes, cache: map[string]evidenceArtifact{}, documents: map[string]*evidenceDocument{}, plans: map[string]evidencePlan{}}
	bundle := &pb.EvidenceBundle{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: h.snapshot.CorpusId, RecordId: "evidence:hydrated"},
		Snapshot: proto.Clone(h.snapshot).(*pb.SnapshotRef), RetrievalManifest: proto.Clone(h.config.Producer).(*pb.ProducerManifest), Completeness: pb.Completeness_COMPLETENESS_COMPLETE, CompletionStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
	result := &HydratedEvidence{Evidence: bundle, Rejected: map[string]string{}}
	urls := map[string][]string{}
	missing := map[string]bool{}
	remaining := h.config.MaximumEvidenceBytes
	for i, entry := range records {
		r := entry.Record
		hit := hits[i]
		if r == nil || r.Meta == nil || r.Meta.RecordId != hit.RecordID || entry.PointID != hit.PointID || r.ChunkId != hit.ChunkID || !sameEvidenceVersions(r.ProvisionVersionRefs, hit.ProvisionVersionIDs) {
			return nil, errors.New("search candidate differs from authoritative index record")
		}
		doc, err := l.source(bounded, r, index)
		if err != nil {
			return nil, err
		}
		accept, uncertain, reason, err := filterEvidenceDate(r, request.TemporalScope)
		if err != nil {
			return nil, err
		}
		if !accept {
			result.Rejected[hit.RecordID] = reason
			continue
		}
		chunk := doc.chunks[r.ChunkId]
		text := doc.texts[chunk.TextSpan.TextArtifactId]
		if text == nil {
			return nil, domain.ErrPersistentIntegrity
		}
		raw, err := l.bytes(bounded, text.NormalizedTextRef)
		if err != nil {
			return nil, err
		}
		span := chunk.TextSpan
		if span.EndByte > uint64(len(raw)) || span.StartByte >= span.EndByte || !utf8.Valid(raw) || !utf8.Valid(raw[span.StartByte:span.EndByte]) {
			return nil, errors.New("chunk UTF-8 text span is outside verified text")
		}
		item := &pb.Evidence{Meta: proto.Clone(r.Meta).(*pb.RecordMeta), Text: string(raw[span.StartByte:span.EndByte]), SourceSpans: []*pb.TextSpan{proto.Clone(span).(*pb.TextSpan)}, SnapshotRef: proto.Clone(h.snapshot).(*pb.SnapshotRef), LegalStatus: r.FilterMetadata.ProvisionFilters[0].LegalStatus}
		for _, f := range r.FilterMetadata.ProvisionFilters {
			item.SourceRefs = append(item.SourceRefs, &pb.SourceVersionRef{SourceBlobId: f.SourceBlobId, ProvisionVersionId: f.ProvisionVersionId, RegulationId: f.RegulationId})
			if f.LegalStatus != item.LegalStatus {
				item.LegalStatus = pb.LegalStatus_LEGAL_STATUS_CONFLICT
			}
			if f.LegalInterval.Start.Knowledge == pb.DateKnowledge_DATE_KNOWLEDGE_CONFLICT || f.LegalInterval.End.Knowledge == pb.DateKnowledge_DATE_KNOWLEDGE_CONFLICT {
				item.LegalStatus = pb.LegalStatus_LEGAL_STATUS_CONFLICT
			}
			key := f.SourceBlobId + "\x00" + f.ProvisionVersionId
			trusted := doc.sourceURLs(f.SourceBlobId, h.snapshot.Sequence)
			if len(trusted) == 0 {
				return nil, errors.New("indexed evidence has no verified source URL")
			}
			urls[key] = trusted
		}
		for _, page := range text.PageResults {
			for _, s := range page.Spans {
				if s.TextArtifactId == span.TextArtifactId && s.StartByte < span.EndByte && s.EndByte > span.StartByte {
					if page.Status != pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED {
						return nil, errors.New("evidence intersects incomplete source page")
					}
					item.Locators = append(item.Locators, &pb.PageLocator{SourceBlobId: text.SourceBlobId, PageNumber: page.PageNumber})
					break
				}
			}
		}
		for _, id := range append(append([]string(nil), chunk.ParentRefs...), chunk.ExceptionRefs...) {
			missing[id] = true
			item.ParentRefs = append(item.ParentRefs, id)
		}
		if uncertain {
			if item.LegalStatus != pb.LegalStatus_LEGAL_STATUS_CONFLICT {
				item.LegalStatus = pb.LegalStatus_LEGAL_STATUS_UNKNOWN
			}
			digest := sha256.Sum256([]byte(r.Meta.RecordId))
			missing["temporal-review:"+hex.EncodeToString(digest[:])] = true
		}
		size := uint64(proto.Size(item))
		if size > remaining {
			return nil, errors.New("hydrated evidence exceeds output byte budget")
		}
		remaining -= size
		bundle.Items = append(bundle.Items, item)
	}
	for id := range missing {
		bundle.MissingDependencies = append(bundle.MissingDependencies, id)
	}
	sort.Strings(bundle.MissingDependencies)
	if len(missing) > 0 {
		bundle.Completeness = pb.Completeness_COMPLETENESS_PARTIAL
	} else if len(bundle.Items) == 0 {
		bundle.Completeness = pb.Completeness_COMPLETENESS_NONE
	}
	if uint64(proto.Size(bundle)) > h.config.MaximumEvidenceBytes {
		return nil, errors.New("hydrated bundle exceeds output byte budget")
	}
	if err = domain.ValidateWire(bundle, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	result.SourceURLs = func(blob, version string) ([]string, error) {
		list, ok := urls[blob+"\x00"+version]
		if !ok {
			return nil, errors.New("source URL is outside hydrated evidence")
		}
		return append([]string(nil), list...), nil
	}
	// Source reads may have outlived or raced revocation of the lease. Never return
	// evidence after losing the pin; the query owner keeps it through generation.
	if _, err = h.catalog.LoadPinnedIndex(bounded, h.pin); err != nil {
		return nil, err
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func sameEvidenceVersions(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
		delete(seen, v)
	}
	return len(seen) == 0
}

type evidenceArtifact struct {
	ref *pb.ArtifactRef
	raw []byte
}
type evidenceDocument struct {
	source *pb.DocumentBatch
	view   *domain.IndexSourceView
	chunks map[string]*pb.Chunk
	texts  map[string]*pb.TextArtifact
}
type evidencePlan struct {
	ref    *pb.ArtifactRef
	plan   *pb.IndexBuildPlan
	chunks map[string]string
}
type evidenceLoader struct {
	catalog   EvidenceCatalog
	reader    EvidenceArtifactReader
	corpus    string
	remaining uint64
	cache     map[string]evidenceArtifact
	documents map[string]*evidenceDocument
	plans     map[string]evidencePlan
}

func (l *evidenceLoader) bytes(ctx context.Context, ref *pb.ArtifactRef) ([]byte, error) {
	if ref == nil {
		return nil, errors.New("missing evidence artifact")
	}
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return nil, err
	}
	if saved, ok := l.cache[ref.ArtifactId]; ok {
		if !proto.Equal(saved.ref, ref) {
			return nil, domain.ErrPersistentIntegrity
		}
		return saved.raw, nil
	}
	if ref.ByteSize == 0 || ref.ByteSize > 16<<20 || ref.ByteSize > l.remaining {
		return nil, errors.New("evidence artifact budget exceeded")
	}
	registered, err := l.catalog.LoadArtifact(ctx, l.corpus, ref.ArtifactId)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(registered, ref) {
		return nil, domain.ErrPersistentIntegrity
	}
	raw, err := l.reader.ReadVerified(ctx, ref, ref.ByteSize)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	if uint64(len(raw)) != ref.ByteSize || hex.EncodeToString(hash[:]) != ref.ContentHash.Sha256 {
		return nil, domain.ErrPersistentIntegrity
	}
	l.remaining -= ref.ByteSize
	l.cache[ref.ArtifactId] = evidenceArtifact{proto.Clone(ref).(*pb.ArtifactRef), raw}
	return raw, nil
}
func (l *evidenceLoader) source(ctx context.Context, r *pb.IndexRecord, index *domain.PinnedIndex) (*evidenceDocument, error) {
	if err := domain.ValidatePairedIndexFilters(r); err != nil {
		return nil, err
	}
	if r.Meta.CorpusId != l.corpus || r.GenerationId != index.Binding.Generation.Meta.RecordId || r.Dependencies == nil || len(r.Dependencies.Dependencies) != 1 {
		return nil, errors.New("indexed evidence dependency mismatch")
	}
	dep := r.Dependencies.Dependencies[0]
	checked, err := l.plan(ctx, dep.DependencyId, index)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(checked.ref.ContentHash, dep.Fingerprint) {
		return nil, domain.ErrPersistentIntegrity
	}
	plan := checked.plan
	if checked.chunks[r.Meta.RecordId] != r.ChunkId {
		return nil, errors.New("record not selected by source plan")
	}
	doc := l.documents[plan.DocumentBatch.ArtifactId]
	if doc == nil {
		raw, err := l.bytes(ctx, plan.DocumentBatch)
		if err != nil {
			return nil, err
		}
		source := new(pb.DocumentBatch)
		if err = domain.DecodeWire(raw, source, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
		if source.Meta.RecordId != plan.DocumentBatch.ArtifactId || source.Meta.CorpusId != l.corpus || !proto.Equal(source.Context.SnapshotRef, plan.SourceSnapshot) || !proto.Equal(plan.SourceSnapshot, index.Snapshot) {
			return nil, errors.New("source evidence does not belong to pinned initial snapshot")
		}
		view, err := domain.NewIndexSourceView(source, 1_000_000)
		if err != nil {
			return nil, err
		}
		doc = &evidenceDocument{source: source, view: view, chunks: map[string]*pb.Chunk{}, texts: map[string]*pb.TextArtifact{}}
		for _, chunk := range source.Chunks {
			doc.chunks[chunk.Meta.RecordId] = chunk
		}
		for _, text := range source.TextArtifacts {
			doc.texts[text.Meta.RecordId] = text
		}
		l.documents[plan.DocumentBatch.ArtifactId] = doc
	} else if !proto.Equal(l.cache[plan.DocumentBatch.ArtifactId].ref, plan.DocumentBatch) {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = doc.view.ValidateRecord(r); err != nil {
		return nil, err
	}
	return doc, nil
}

// Decode and validate each immutable plan once, then use its record lookup for
// all candidates in this request. The cache never survives a request or pin.
func (l *evidenceLoader) plan(ctx context.Context, id string, index *domain.PinnedIndex) (evidencePlan, error) {
	if saved, ok := l.plans[id]; ok {
		return saved, nil
	}
	ref, err := l.catalog.LoadArtifact(ctx, l.corpus, id)
	if err != nil {
		return evidencePlan{}, err
	}
	if ref == nil || ref.ArtifactId != id || ref.MediaType != domain.IndexBuildPlanMediaType {
		return evidencePlan{}, domain.ErrPersistentIntegrity
	}
	raw, err := l.bytes(ctx, ref)
	if err != nil {
		return evidencePlan{}, err
	}
	plan := new(pb.IndexBuildPlan)
	if err = domain.DecodeWire(raw, plan, domain.DefaultWireLimits); err != nil {
		return evidencePlan{}, err
	}
	if err = domain.ValidateIndexBuildPlan(plan); err != nil {
		return evidencePlan{}, err
	}
	if plan.Meta.RecordId != id || !proto.Equal(plan.Generation, index.Binding.Generation) || !proto.Equal(plan.TargetSnapshot, index.Snapshot) {
		return evidencePlan{}, errors.New("evidence build plan differs from published snapshot")
	}
	checked := evidencePlan{ref: proto.Clone(ref).(*pb.ArtifactRef), plan: plan, chunks: map[string]string{}}
	for _, item := range plan.Items {
		checked.chunks[item.RecordId] = item.ChunkId
	}
	l.plans[id] = checked
	return checked, nil
}
func (d *evidenceDocument) sourceURLs(blob string, sequence uint64) []string {
	set := map[string]bool{}
	for _, o := range d.source.Observations {
		if o.GetSourceBlobId() != blob || o.Status != pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE || o.Meta.Visibility != nil && (o.Meta.Visibility.FromSeq > sequence || o.Meta.Visibility.ToSeq != nil && *o.Meta.Visibility.ToSeq <= sequence) {
			continue
		}
		for _, raw := range []string{o.ResolvedUrl, o.DetailUrl} {
			u, err := url.Parse(raw)
			if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil {
				set[raw] = true
			}
		}
	}
	urls := make([]string, 0, len(set))
	for raw := range set {
		urls = append(urls, raw)
	}
	sort.Strings(urls)
	if len(urls) > 32 {
		return nil
	}
	return urls
}

// Date filtering interprets only explicit interval assertions. It never infers
// effective dates from observation dates or treats UNKNOWN as unbounded.
func filterEvidenceDate(r *pb.IndexRecord, scope *pb.TemporalScope) (bool, bool, string, error) {
	dateKey := func(d *pb.CalendarDate) int64 { return int64(d.Year)*10000 + int64(d.Month)*100 + int64(d.Day) }
	date := dateKey(scope.EffectiveAt)
	uncertain := false
	included, excluded := 0, 0
	for _, f := range r.FilterMetadata.ProvisionFilters {
		start, end := f.LegalInterval.Start, f.LegalInterval.End
		if start.Knowledge == pb.DateKnowledge_DATE_KNOWLEDGE_KNOWN && date < dateKey(start.Value) || end.Knowledge == pb.DateKnowledge_DATE_KNOWLEDGE_KNOWN && date >= dateKey(end.Value) {
			excluded++
			continue
		}
		unresolved := false
		for _, bound := range []*pb.DateAssertion{start, end} {
			if bound.Knowledge != pb.DateKnowledge_DATE_KNOWLEDGE_KNOWN && bound.Knowledge != pb.DateKnowledge_DATE_KNOWLEDGE_UNBOUNDED {
				unresolved = true
			}
		}
		unresolved = unresolved || f.LegalStatus == pb.LegalStatus_LEGAL_STATUS_UNKNOWN || f.LegalStatus == pb.LegalStatus_LEGAL_STATUS_CONFLICT ||
			f.LegalStatus == pb.LegalStatus_LEGAL_STATUS_REPEALED && end.Knowledge != pb.DateKnowledge_DATE_KNOWLEDGE_KNOWN ||
			f.LegalStatus == pb.LegalStatus_LEGAL_STATUS_NOT_YET_EFFECTIVE && start.Knowledge != pb.DateKnowledge_DATE_KNOWLEDGE_KNOWN
		if unresolved {
			switch scope.UnresolvedPolicy {
			case pb.UnresolvedPolicy_UNRESOLVED_POLICY_EXCLUDE:
				excluded++
				continue
			case pb.UnresolvedPolicy_UNRESOLVED_POLICY_REQUIRE_REVIEW:
				return false, true, "", errors.New("legal applicability requires review")
			case pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT:
				uncertain = true
			default:
				return false, true, "", errors.New("unsupported unresolved-date policy")
			}
		}
		included++
	}
	if included > 0 && excluded > 0 {
		return false, false, "", errors.New("mixed-version applicability requires version-level temporal text projection")
	}
	if included == 0 {
		return false, false, "no version eligible under requested date and unresolved policy", nil
	}
	for _, f := range r.FilterMetadata.ProvisionFilters[1:] {
		if f.LegalStatus != r.FilterMetadata.ProvisionFilters[0].LegalStatus {
			switch scope.UnresolvedPolicy {
			case pb.UnresolvedPolicy_UNRESOLVED_POLICY_EXCLUDE:
				return false, false, "mixed legal statuses require version-level temporal text projection", nil
			case pb.UnresolvedPolicy_UNRESOLVED_POLICY_REQUIRE_REVIEW:
				return false, true, "", errors.New("mixed legal statuses require review")
			case pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT:
				uncertain = true
			}
		}
	}
	return true, uncertain, "", nil
}
